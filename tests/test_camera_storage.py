"""Read-only C201 storage sensors keep unverified units and meanings raw."""
from __future__ import annotations

import asyncio
from tempfile import TemporaryDirectory
import unittest
from unittest.mock import patch

from gateway_test_path import add_gateway_component_path

add_gateway_component_path()

from xiaomi_local_gateway import sensor
from xiaomi_local_gateway.coordinator import CameraSettingsSupport, CameraStorageCoordinator
from homeassistant.core import HomeAssistant


class FakeCameraCoordinator:
    def __init__(self):
        self.data = {
            "c201": {"id": "c201", "name": "Test C201", "model": "xiaomi.camera.c201"},
            "other": {"id": "other", "name": "Other camera", "model": "xiaomi.camera.other"},
        }
        self.last_update_success = True
        self.settings_coordinator = None
        self.storage_coordinator = None
        self.listeners = []

    def async_add_listener(self, callback):
        self.listeners.append(callback)
        return lambda: self.listeners.remove(callback) if callback in self.listeners else None

    def async_update_listeners(self):
        for listener in tuple(self.listeners):
            listener()


class FakeEntry:
    def __init__(self, runtime_data):
        self.runtime_data = runtime_data
        self.unload_callbacks = []

    def async_on_unload(self, callback):
        self.unload_callbacks.append(callback)


class FakeClient:
    def __init__(self, features, storage=None):
        self.features = set(features)
        self.storage = storage or {}
        self.storage_calls = []
        self.blocked_camera_id = None
        self.storage_wait_started = asyncio.Event()
        self.release_storage = asyncio.Event()

    async def camera_read_capabilities(self):
        return self.features

    async def camera_storage(self, camera_id):
        self.storage_calls.append(camera_id)
        if camera_id == self.blocked_camera_id:
            self.storage_wait_started.set()
            await self.release_storage.wait()
        return self.storage


class CameraStorageEntityTests(unittest.IsolatedAsyncioTestCase):
    async def test_advertised_support_adds_four_raw_entities_without_guessing_units(self):
        cameras = FakeCameraCoordinator()
        entry = FakeEntry(cameras)
        storage = {
            "observed_at": "2026-11-13T09:00:00Z",
            "properties": {
                "card_status": {"code": 0, "value": 1, "unit": None},
                "total": {"code": 0, "value": 0, "unit": None},
                "free": {"code": -704040003, "value": None, "unit": None},
                "used": {"code": 0, "value": 2147483648, "unit": None},
            },
        }
        client = FakeClient({"sd_capacity"}, storage)
        entities = []
        await sensor.async_setup_entry(None, entry, entities.extend)
        self.assertEqual(entities, [])

        with TemporaryDirectory() as config_dir:
            hass = HomeAssistant(config_dir)
            await hass.async_start()
            support = CameraSettingsSupport(hass, None, client, cameras)
            support.start()
            try:
                await support._task
                self.assertEqual(client.storage_calls, ["c201"])
                self.assertIsNotNone(cameras.storage_coordinator)
                self.assertIsNone(cameras.settings_coordinator)
                self.assertEqual(len(entities), 4)
                self.assertEqual({entity._camera_id for entity in entities}, {"c201"})
                by_key = {entity._key: entity for entity in entities}
                self.assertTrue(by_key["card_status"].available)
                self.assertEqual(by_key["card_status"].native_value, 1)
                self.assertIn("raw", by_key["card_status"].name.lower())
                self.assertEqual(by_key["card_status"].extra_state_attributes["unit"], None)
                self.assertTrue(by_key["total"].available)
                self.assertEqual(by_key["total"].native_value, 0)
                self.assertEqual(by_key["total"].native_unit_of_measurement, None)
                self.assertFalse(by_key["free"].available)
                self.assertIsNone(by_key["free"].native_value)
                self.assertTrue(by_key["used"].available)
                self.assertEqual(by_key["used"].native_value, 2147483648)
                self.assertEqual(client.storage_calls.count("other"), 0)

                # An omitted property remains unavailable instead of turning
                # into a zero reading or suppressing the other raw sensors.
                client.storage["properties"].pop("used")
                await cameras.storage_coordinator.async_refresh()
                self.assertFalse(by_key["used"].available)
                self.assertIsNone(by_key["used"].native_value)

                # Storage values must follow camera inventory/availability
                # changes immediately, without waiting for another /storage
                # refresh.
                card = by_key["card_status"]
                storage_call_count = len(client.storage_calls)
                card.hass = hass
                with patch.object(card, "async_write_ha_state") as write_state:
                    await card.async_added_to_hass()
                    try:
                        cameras.last_update_success = False
                        cameras.async_update_listeners()
                        self.assertFalse(card.available)
                        write_state.assert_called_once_with()

                        cameras.last_update_success = True
                        cameras.data.pop("c201")
                        cameras.async_update_listeners()
                        self.assertFalse(card.available)
                        self.assertEqual(write_state.call_count, 2)

                        # A return before the next storage poll must reuse its
                        # still-valid snapshot without another /storage call.
                        cameras.data["c201"] = {
                            "id": "c201", "name": "Test C201", "model": "xiaomi.camera.c201"
                        }
                        cameras.async_update_listeners()
                        await hass.async_block_till_done()
                        self.assertTrue(card.available)
                        self.assertEqual(card.native_value, 1)
                        self.assertEqual(len(client.storage_calls), storage_call_count)

                        cameras.data.pop("c201")
                        cameras.async_update_listeners()
                        self.assertFalse(card.available)

                        # A scheduled SD poll can complete while the camera is
                        # absent, leaving no cached snapshot for its return.
                        await cameras.storage_coordinator.async_refresh()
                        self.assertNotIn("c201", cameras.storage_coordinator.data)
                        self.assertEqual(len(client.storage_calls), storage_call_count)
                        writes_after_absent_poll = write_state.call_count
                        self.assertGreater(writes_after_absent_poll, 2)

                        cameras.data["c201"] = {
                            "id": "c201", "name": "Test C201", "model": "xiaomi.camera.c201"
                        }
                        cameras.async_update_listeners()
                        writes_after_camera_return = write_state.call_count
                        self.assertGreater(writes_after_camera_return, writes_after_absent_poll)
                        await cameras.storage_coordinator._camera_refresh_tasks["c201"]
                        self.assertTrue(card.available)
                        self.assertEqual(card.native_value, 1)
                        self.assertEqual(len(client.storage_calls), storage_call_count + 1)
                    finally:
                        await card.async_will_remove_from_hass()
            finally:
                support.stop()
                for unsubscribe in entry.unload_callbacks:
                    unsubscribe()
                if cameras.storage_coordinator is not None:
                    await cameras.storage_coordinator.async_shutdown()
                await hass.async_stop()

    async def test_return_during_storage_poll_reads_after_poll_publication(self):
        cameras = FakeCameraCoordinator()
        entry = FakeEntry(cameras)
        storage = {
            "observed_at": "2026-11-13T09:00:00Z",
            "properties": {"card_status": {"code": 0, "value": 1, "unit": None}},
        }
        client = FakeClient({"sd_capacity"}, storage)
        entities = []
        await sensor.async_setup_entry(None, entry, entities.extend)

        with TemporaryDirectory() as config_dir:
            hass = HomeAssistant(config_dir)
            await hass.async_start()
            support = CameraSettingsSupport(hass, None, client, cameras)
            support.start()
            try:
                await support._task
                card = next(entity for entity in entities if entity._key == "card_status")
                card.hass = hass
                await card.async_added_to_hass()
                try:
                    with patch.object(card, "async_write_ha_state"):
                        self.assertTrue(card.available)
                        self.assertEqual(client.storage_calls, ["c201"])

                        # Make another C201's storage response hold the poll open.
                        cameras.data.pop("c201")
                        cameras.data["blocker"] = {
                            "id": "blocker", "name": "Slow C201", "model": "xiaomi.camera.c201"
                        }
                        client.blocked_camera_id = "blocker"
                        poll = hass.async_create_task(cameras.storage_coordinator.async_refresh())
                        await asyncio.wait_for(client.storage_wait_started.wait(), timeout=2)

                        # The previous storage snapshot still contains c201 here;
                        # its return must nevertheless queue a post-poll recheck.
                        cameras.data["c201"] = {
                            "id": "c201", "name": "Test C201", "model": "xiaomi.camera.c201"
                        }
                        cameras.async_update_listeners()
                        refresh = cameras.storage_coordinator._camera_refresh_tasks.get("c201")
                        self.assertIsNotNone(refresh)
                        self.assertFalse(refresh.done())

                        client.release_storage.set()
                        await asyncio.wait_for(poll, timeout=2)
                        await asyncio.wait_for(refresh, timeout=2)

                        self.assertTrue(card.available)
                        self.assertEqual(card.native_value, 1)
                        self.assertEqual(client.storage_calls, ["c201", "blocker", "c201"])
                finally:
                    await card.async_will_remove_from_hass()
            finally:
                support.stop()
                for unsubscribe in entry.unload_callbacks:
                    unsubscribe()
                if cameras.storage_coordinator is not None:
                    await cameras.storage_coordinator.async_shutdown()
                await hass.async_stop()

    async def test_return_during_scheduled_timer_poll_reads_after_publication(self):
        cameras = FakeCameraCoordinator()
        cameras.data = {
            "c201": {"id": "c201", "name": "Test C201", "model": "xiaomi.camera.c201"},
            "blocker": {"id": "blocker", "name": "Slow C201", "model": "xiaomi.camera.c201"},
        }
        entry = FakeEntry(cameras)
        storage = {
            "observed_at": "2026-11-13T09:00:00Z",
            "properties": {"card_status": {"code": 0, "value": 1, "unit": None}},
        }
        client = FakeClient({"sd_capacity"}, storage)
        entities = []

        with TemporaryDirectory() as config_dir:
            hass = HomeAssistant(config_dir)
            await hass.async_start()
            coordinator = CameraStorageCoordinator(hass, None, client, cameras)
            # Exercise HA's actual scheduled timer path without waiting five minutes.
            coordinator._update_interval_seconds = 0.05
            cameras.storage_coordinator = coordinator
            await sensor.async_setup_entry(None, entry, entities.extend)
            card = next(entity for entity in entities if entity._key == "card_status")
            card.hass = hass
            try:
                with patch.object(card, "async_write_ha_state"):
                    await coordinator.async_refresh()
                    self.assertEqual(client.storage_calls, ["c201", "blocker"])

                    # Register a listening HA entity after the successful first
                    # poll; adding the listener starts the real HA timer.
                    await card.async_added_to_hass()
                    self.assertIsNotNone(coordinator._unsub_refresh)

                    cameras.data.pop("c201")
                    cameras.async_update_listeners()
                    self.assertFalse(card.available)

                    client.blocked_camera_id = "blocker"
                    client.storage_wait_started.clear()
                    client.release_storage.clear()
                    # This wait is released by the coordinator's scheduled HA
                    # callback, not by a direct call to async_refresh().
                    await asyncio.wait_for(client.storage_wait_started.wait(), timeout=3)
                    self.assertTrue(coordinator._storage_lock.locked())

                    cameras.data["c201"] = {
                        "id": "c201", "name": "Test C201", "model": "xiaomi.camera.c201"
                    }
                    cameras.async_update_listeners()
                    refresh = coordinator._camera_refresh_tasks.get("c201")
                    self.assertIsNotNone(refresh)
                    self.assertFalse(refresh.done())

                    client.release_storage.set()
                    await asyncio.wait_for(refresh, timeout=3)
                    self.assertTrue(card.available)
                    self.assertEqual(card.native_value, 1)
                    self.assertEqual(client.storage_calls, ["c201", "blocker", "blocker", "c201"])
            finally:
                client.release_storage.set()
                await card.async_will_remove_from_hass()
                await coordinator.async_shutdown()
                await hass.async_stop()

    async def test_first_storage_poll_return_is_read_after_sensor_registration(self):
        cameras = FakeCameraCoordinator()
        cameras.data = {
            "blocker": {"id": "blocker", "name": "Slow C201", "model": "xiaomi.camera.c201"},
        }
        entry = FakeEntry(cameras)
        storage = {
            "observed_at": "2026-11-13T09:00:00Z",
            "properties": {"card_status": {"code": 0, "value": 1, "unit": None}},
        }
        client = FakeClient({"sd_capacity"}, storage)
        client.blocked_camera_id = "blocker"
        entities = []
        await sensor.async_setup_entry(None, entry, entities.extend)

        with TemporaryDirectory() as config_dir:
            hass = HomeAssistant(config_dir)
            await hass.async_start()
            support = CameraSettingsSupport(hass, None, client, cameras)
            support.start()
            try:
                await asyncio.wait_for(client.storage_wait_started.wait(), timeout=2)
                # C201 returns while the very first storage read is waiting on
                # another camera; no storage coordinator/entities are exposed yet.
                cameras.data["c201"] = {
                    "id": "c201", "name": "Test C201", "model": "xiaomi.camera.c201"
                }
                cameras.async_update_listeners()
                client.release_storage.set()
                await asyncio.wait_for(support._task, timeout=2)

                coordinator = cameras.storage_coordinator
                self.assertIsNotNone(coordinator)
                self.assertNotIn("c201", coordinator.data)
                self.assertEqual(client.storage_calls, ["blocker"])
                self.assertIn("c201", cameras.data)
                self.assertTrue(cameras.last_update_success)

                card = next(
                    entity for entity in entities
                    if entity._camera_id == "c201" and entity._key == "card_status"
                )
                card.hass = hass
                with patch.object(card, "async_write_ha_state"), patch.object(
                    coordinator, "request_missing_camera", wraps=coordinator.request_missing_camera
                ) as request_missing:
                    await card.async_added_to_hass()
                    request_missing.assert_called_once_with("c201")
                    refresh = coordinator._camera_refresh_tasks.get("c201")
                    self.assertIsNotNone(refresh)
                    await asyncio.wait_for(refresh, timeout=2)
                    self.assertTrue(card.available)
                    self.assertEqual(card.native_value, 1)
                    self.assertEqual(client.storage_calls, ["blocker", "c201"])
            finally:
                client.release_storage.set()
                support.stop()
                for unsubscribe in entry.unload_callbacks:
                    unsubscribe()
                if cameras.storage_coordinator is not None:
                    await cameras.storage_coordinator.async_shutdown()
                await hass.async_stop()

    async def test_unadvertised_storage_is_not_polled_or_exposed(self):
        cameras = FakeCameraCoordinator()
        entry = FakeEntry(cameras)
        client = FakeClient(set())
        entities = []
        await sensor.async_setup_entry(None, entry, entities.extend)
        with TemporaryDirectory() as config_dir:
            hass = HomeAssistant(config_dir)
            await hass.async_start()
            support = CameraSettingsSupport(hass, None, client, cameras)
            try:
                self.assertFalse(await support.async_check())
                self.assertIsNone(cameras.storage_coordinator)
                self.assertEqual(client.storage_calls, [])
                self.assertEqual(entities, [])
            finally:
                support.stop()
                for unsubscribe in entry.unload_callbacks:
                    unsubscribe()
                await hass.async_stop()


if __name__ == "__main__":
    unittest.main()
