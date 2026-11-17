"""Wire-level checks with a synthetic camera on loopback."""

import asyncio
from ipaddress import IPv4Address, IPv4Network
import json
import socket
import struct
import unittest
from unittest.mock import patch

from cryptography.hazmat.primitives.ciphers import Cipher, algorithms
from nacl.bindings import crypto_box_beforenm, crypto_scalarmult_base

from custom_components.xiaomi_direct_pilot.xiaomi_direct import c201_session, cs2_tcp


def wrapper(packet):
    return struct.pack(">HB5x", len(packet), 0x68) + packet


def data(channel, fragment):
    return wrapper(struct.pack(">BBHBBH", 0xF1, 0xD0, 4 + len(fragment), 0xD1, channel, 0) + fragment)


def message(payload):
    return struct.pack(">I", len(payload)) + payload


async def read_packet(reader):
    size, marker = struct.unpack(">HB5x", await reader.readexactly(8))
    assert marker == 0x68
    return await reader.readexactly(size)


def crypt(key, nonce, payload):
    cipher = Cipher(algorithms.ChaCha20(key, bytes(8) + nonce), mode=None).encryptor()
    return cipher.update(payload) + cipher.finalize()


class C201SessionTest(unittest.IsolatedAsyncioTestCase):
    async def test_account_signs_fresh_ephemeral_keys(self):
        requested = []
        camera_key = crypto_scalarmult_base(bytes(range(32, 64)))

        async def account(path, params):
            self.assertEqual(path, "/v2/device/miss_get_vendor")
            self.assertEqual(params["did"], "mock-c201")
            requested.append(params["app_pubkey"])
            return {"vendor": {"vendor": 4}, "public_key": camera_key.hex(), "sign": "mock-sign"}

        for index in range(2):
            credentials = await c201_session.request_signed_session(account, "mock-c201")
            self.assertEqual(requested[index], crypto_scalarmult_base(credentials.private_key).hex())
            self.assertEqual(credentials.camera_public_key, camera_key)
            self.assertNotIn("mock-sign", repr(credentials))
        self.assertNotEqual(*requested)

    async def test_discovery_follows_camera_port_and_rejects_udp_nomination(self):
        loop = asyncio.get_running_loop()
        for ready in (0x43, 0x42):
            with (
                self.subTest(ready=ready),
                socket.socket(socket.AF_INET, socket.SOCK_DGRAM) as search,
                socket.socket(socket.AF_INET, socket.SOCK_DGRAM) as punch,
                socket.socket(socket.AF_INET, socket.SOCK_DGRAM) as client,
            ):
                for sock in (search, punch, client):
                    sock.bind(("127.0.0.1", 0))
                    sock.setblocking(False)

                async def camera():
                    request, peer = await loop.sock_recvfrom(search, 100)
                    self.assertEqual(request, bytes.fromhex("f1300000"))
                    await loop.sock_sendto(punch, bytes.fromhex("f1410001"), peer)
                    await loop.sock_sendto(punch, bytes.fromhex("f141000100"), peer)
                    echo, endpoint = await loop.sock_recvfrom(punch, 100)
                    self.assertEqual((echo, endpoint), (bytes.fromhex("f141000100"), peer))
                    await loop.sock_sendto(punch, bytes((0xF1, ready, 0, 0)), peer)

                async with asyncio.timeout(3), asyncio.TaskGroup() as group:
                    group.create_task(camera())
                    args = (client, "127.0.0.1", None, 1)
                    if ready == 0x43:
                        peer = await cs2_tcp._negotiate(*args, port=search.getsockname()[1])
                        self.assertEqual(peer, punch.getsockname())
                    else:
                        with self.assertRaisesRegex(cs2_tcp.CS2Error, "nominated CS2/UDP"):
                            await cs2_tcp._negotiate(*args, port=search.getsockname()[1])

    async def test_signed_login_video_and_reopen(self):
        client_secret = bytes(range(32))
        camera_secret = bytes(range(32, 64))
        credentials = c201_session.SignedSession(client_secret, crypto_scalarmult_base(camera_secret), "mock-sign")
        key = crypto_box_beforenm(crypto_scalarmult_base(client_secret), camera_secret)
        nonce = b"12345678"
        frame = bytes.fromhex("000000012601aa")
        opens = []
        handlers = asyncio.Queue()

        async def camera(reader, writer):
            try:
                auth = await read_packet(reader)
                self.assertEqual(auth[:8], bytes.fromhex("f1d0") + struct.pack(">H", len(auth)-4) + bytes.fromhex("d1000000"))
                self.assertEqual(auth[12:16], bytes.fromhex("00000100"))
                self.assertEqual(json.loads(auth[16:])["sign"], "mock-sign")
                self.assertEqual(json.loads(auth[16:])["public_key"], crypto_scalarmult_base(client_secret).hex())
                writer.write(data(0, message(b'\x00\x00\x01\x01{"result":"success"}')))
                await writer.drain()
                start = await read_packet(reader)
                self.assertEqual(start[6:8], b"\x00\x01")
                self.assertEqual(start[12:16], bytes.fromhex("00001001"))
                self.assertEqual(crypt(key, start[16:24], start[24:]), b'\x00\x00\x01\x02{"videoquality":0,"enableaudio":0}')
                encrypted = nonce + crypt(key, nonce, frame)
                media = message(struct.pack("<4xIIIQ8x", 5, 7, 0, 1000) + encrypted)
                writer.write(wrapper(bytes.fromhex("f1e00000")) + data(2, media[:3]) + data(2, media[3:]))
                await writer.drain()
                self.assertEqual(await read_packet(reader), bytes.fromhex("f1e10000"))
                ptz = await read_packet(reader)
                self.assertEqual(ptz[12:16], bytes.fromhex("00001001"))
                self.assertEqual(crypt(key, ptz[16:24], ptz[24:]), b'\x00\x00\x01\x12{"operation":1}')
                stop = await read_packet(reader)
                self.assertEqual(stop[12:16], bytes.fromhex("00001001"))
                self.assertEqual(crypt(key, stop[16:24], stop[24:]), bytes.fromhex("00000103"))
                self.assertEqual(await reader.read(), b"")
                opens.append(1)
            finally:
                writer.close()
                await writer.wait_closed()

        server = await asyncio.start_server(lambda r, w: handlers.put_nowait(asyncio.create_task(camera(r, w))), "127.0.0.1", 0)

        async def connect(*args, **kwargs):
            return cs2_tcp.TCPTransport(*await asyncio.open_connection(*server.sockets[0].getsockname()))

        async with server, asyncio.timeout(5):
            with patch.object(c201_session, "open_transport", new=connect):
                for _ in range(2):
                    async with c201_session.open_video(None, None, None, credentials) as video:
                        received = await video.read(timeout=1)
                        self.assertEqual((received.codec, received.sequence, received.payload), ("h265", 7, frame))
                        self.assertTrue(received.random_access)
                        await video.step(1)
                    await (await handlers.get())
        self.assertEqual(len(opens), 2)

    async def test_discovery_rejects_non_lan_target(self):
        with self.assertRaisesRegex(cs2_tcp.CS2Error, "selected private LAN"):
            await cs2_tcp.TCPTransport.open(
                IPv4Address("8.8.8.8"), IPv4Address("8.8.8.9"), IPv4Network("8.8.8.0/24")
            )
