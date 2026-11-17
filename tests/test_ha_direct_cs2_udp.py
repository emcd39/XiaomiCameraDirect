import asyncio
import socket
import unittest

from custom_components.xiaomi_direct_pilot.xiaomi_direct.cs2_tcp import (
    CS2Error, UDPTransport, _DATA, _SIZE, _frame,
)


class FakeSocket:
    def __init__(self):
        self.sent = []
        self.closed = False

    def close(self):
        self.closed = True


class CS2UDPTest(unittest.IsolatedAsyncioTestCase):
    async def test_command_wait_acknowledges_fragmented_incoming_data(self):
        loop = asyncio.get_running_loop()
        with (
            socket.socket(socket.AF_INET, socket.SOCK_DGRAM) as client,
            socket.socket(socket.AF_INET, socket.SOCK_DGRAM) as camera,
        ):
            for sock in (client, camera):
                sock.bind(("127.0.0.1", 0))
                sock.setblocking(False)

            async def reply():
                command, peer = await loop.sock_recvfrom(camera, 100)
                self.assertEqual(command[1], 0xD0)
                content = _SIZE.pack(5) + b"media"
                for sequence, part in enumerate((content[:2], content[2:])):
                    packet = _frame(0xD0, _DATA.pack(0xD1, 2, sequence) + part)
                    await loop.sock_sendto(camera, packet, peer)
                    ack, _ = await loop.sock_recvfrom(camera, 100)
                    self.assertEqual(ack, _frame(0xD1, b"\xd1\x02\x00\x01" + sequence.to_bytes(2, "big")))
                await loop.sock_sendto(camera, _frame(0xD1, b"\xd1\x00\x00\x01\x00\x00"), peer)

            async with asyncio.timeout(3), asyncio.TaskGroup() as group:
                group.create_task(reply())
                transport = UDPTransport(client, camera.getsockname())
                await transport.send_command(0x100, b"login")
                self.assertEqual(await transport.receive(), (2, b"media"))

    async def test_out_of_order_packets_reassemble_and_ack(self):
        sock = FakeSocket()
        transport = UDPTransport(sock, ("192.168.1.2", 1234))
        msg = b"hello"
        wire = _SIZE.pack(len(msg)) + msg
        transport._ingest(_frame(0xD0, _DATA.pack(0xD1, 0, 1) + wire))
        transport._ingest(_frame(0xD0, _DATA.pack(0xD1, 0, 0) + wire))
        self.assertEqual(await transport.receive(), (0, msg))
        self.assertEqual(await transport.receive(), (0, msg))

    async def test_message_size_is_bounded(self):
        transport = UDPTransport(FakeSocket(), ("192.168.1.2", 1234))
        with self.assertRaises(CS2Error):
            transport._push(0, _SIZE.pack(100000) + b"x")

    async def test_context_exit_closes_socket(self):
        sock = FakeSocket()
        transport = UDPTransport(sock, ("192.168.1.2", 1234))
        async with transport:
            pass
        self.assertTrue(sock.closed)
