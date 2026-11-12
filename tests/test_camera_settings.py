"""Focused Home Assistant tests for read-only C201 settings entities."""
from __future__ import annotations

import asyncio
from pathlib import Path
import sys
from tempfile import TemporaryDirectory
import unittest

sys.path.insert(0, str(Path(__file__).resolve().parents[1] / "custom_components"))

from xiaomi_local_gateway import binary_sensor, sensor
from xiaomi_local_gateway.api import GatewayError
from xiaomi_local_gateway.coordinator import CameraSettingsSupport
from homeassistant.core import HomeAssistant


class FakeSettingsCoordinator:
    def __init__(self, data):
        self.data = data
        self.last_update_success = True
        self.listeners = []
        self.refreshed = False

    def async_add_listener(self, callback):
        self.listeners.append(callback)
        return lambda: self.listeners.remove(callback) if callback in self.listeners else None

    async def async_config_entry_first_refresh(self):
        self.refreshed = True


class FakeCameraCoordinator:
    def __init__(self):
        self.data = {
            "c201": {"id": "c201", "name": "Test C201", "model": "xiaomi.camera.c201"},
            "other": {"id": "other", "name": "Other camera", "model": "xiaomi.camera.other"},
        }
        self.last_update_success = True
        self.settings_coordinator = None
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
    def __init__(self, responses, settings_response=None):
        self.responses = list(responses)
        self.calls = 0
        self.settings_response = settings_response or {"observed_at": "now", "properties": {}}
        self.settings_calls = []

    async def supports_camera_settings(self):
        self.calls += 1
        response = self.responses.pop(0)
        if isinstance(response, Exception):
            raise response
        return response

    async def camera_settings(self, camera_id):
        self.settings_calls.append(camera_id)
        return self.settings_response


class CameraSettingsEntityTests(unittest.IsolatedAsyncioTestCase):
    async def test_late_settings_support_discovers_one_sensor_and_five_binary_sensors(self):
        cameras = FakeCameraCoordinator()
        entry = FakeEntry(cameras)
        night_entities = []
        enabled_entities = []
        await sensor.async_setup_entry(None, entry, night_entities.extend)
        await binary_sensor.async_setup_entry(None, entry, enabled_entities.extend)
        self.assertEqual(night_entities, [])
        self.assertEqual(enabled_entities, [])

        properties = {
            "night_shot": {"code": 0, "value": 2},
            "indicator_light": {"code": 0, "value": True},
            "motion_detection": {"code": 0, "value": True},
            "human_detection": {"code": 0, "value": False},
            "move_detection": {"code": 0, "value": True},
            "pet_detection": {"code": 0, "value": False},
        }
        settings = FakeSettingsCoordinator({
            "c201": {"observed_at": "2026-11-12T12:00:00Z", "properties": properties},
        })
        cameras.settings_coordinator = settings
        cameras.async_update_listeners()

        self.assertEqual(len(night_entities), 1)
        self.assertEqual(len(enabled_entities), 5)
        self.assertEqual({entity._camera_id for entity in night_entities + enabled_entities}, {"c201"})

        night = night_entities[0]
        self.assertTrue(night.available)
        self.assertEqual(night.native_value, "Auto")
        for value, label in ((0, "Off"), (1, "On"), (2, "Auto"), (9, "Unknown (9)")):
            properties["night_shot"] = {"code": 0, "value": value}
            self.assertEqual(night.native_value, label)
        properties["night_shot"] = {"code": -704040003, "value": None}
        self.assertFalse(night.available)
        self.assertIsNone(night.native_value)

        motion = next(entity for entity in enabled_entities if entity._key == "motion_detection")
        self.assertTrue(motion.available)
        self.assertTrue(motion.is_on)
        properties["motion_detection"] = {"code": 0, "value": False}
        self.assertFalse(motion.is_on)
        properties["motion_detection"] = {"code": -1, "value": None}
        self.assertFalse(motion.available)
        self.assertIsNone(motion.is_on)

    async def test_transient_info_failure_is_retried_without_settings_call_until_advertised(self):
        cameras = FakeCameraCoordinator()
        settings_response = {
            "observed_at": "2026-11-12T12:00:00Z",
            "properties": {
                "night_shot": {"code": 0, "value": 2},
                "indicator_light": {"code": 0, "value": True},
                "motion_detection": {"code": 0, "value": True},
                "human_detection": {"code": 0, "value": False},
                "move_detection": {"code": 0, "value": True},
                "pet_detection": {"code": 0, "value": False},
            },
        }
        client = FakeClient([GatewayError("temporary failure"), True], settings_response)
        entry = FakeEntry(cameras)
        with TemporaryDirectory() as config_dir:
            hass = HomeAssistant(config_dir)
            await hass.async_start()
            try:
                await self._check_support_recovers_in_running_ha(hass, entry, cameras, client)
            finally:
                await hass.async_stop()

    async def _check_support_recovers_in_running_ha(self, hass, entry, cameras, client):
        notifications = []
        night_entities = []
        enabled_entities = []
        cameras.async_add_listener(lambda: notifications.append("updated"))
        await sensor.async_setup_entry(hass, entry, night_entities.extend)
        await binary_sensor.async_setup_entry(hass, entry, enabled_entities.extend)
        self.assertEqual(night_entities, [])
        self.assertEqual(enabled_entities, [])

        support = CameraSettingsSupport(hass, None, client, cameras)
        support.start()
        try:
            await support._task
            self.assertIsNone(cameras.settings_coordinator)
            self.assertEqual(client.calls, 1)
            self.assertEqual(client.settings_calls, [])

            # Simulate the next one-minute support check after the initial failure.
            support._schedule_check()
            await support._task
            self.assertEqual(client.calls, 2)
            self.assertEqual(client.settings_calls, ["c201"])
            self.assertIsNotNone(cameras.settings_coordinator)
            self.assertTrue(cameras.settings_coordinator.last_update_success)
            self.assertEqual(notifications, ["updated"])
            # These platforms were already loaded when the settings support appeared.
            self.assertEqual(len(night_entities), 1)
            self.assertEqual(len(enabled_entities), 5)
            self.assertEqual(night_entities[0].native_value, "Auto")
            self.assertTrue(night_entities[0].available)
            self.assertTrue(next(entity for entity in enabled_entities if entity._key == "motion_detection").is_on)

            response = client.settings_response
            response["properties"]["night_shot"] = {"code": -704040003, "value": None}
            await cameras.settings_coordinator.async_refresh()
            self.assertFalse(night_entities[0].available)
            self.assertIsNone(night_entities[0].native_value)
        finally:
            support.stop()
            for unsubscribe in entry.unload_callbacks:
                unsubscribe()

            await cameras.settings_coordinator.async_shutdown()

    async def test_unadvertised_gateway_does_not_enable_settings_coordinator(self):
        cameras = FakeCameraCoordinator()
        client = FakeClient([False])
        support = CameraSettingsSupport(None, None, client, cameras)
        self.assertFalse(await support.async_check())
        self.assertIsNone(cameras.settings_coordinator)
        self.assertEqual(client.calls, 1)
        self.assertEqual(client.settings_calls, [])


if __name__ == "__main__":
    unittest.main()
