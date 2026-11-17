"""Local CS2/TCP transport, based on the vendored MIT Go reference."""

import asyncio
from collections import deque
from ipaddress import IPv4Address, IPv4Network
import socket
from struct import Struct


_WRAPPER = Struct(">HB5x")
_FRAME = Struct(">BBH")
_DATA = Struct(">BBH")
_SIZE = Struct(">I")
_LAN_SEARCH = b"\xf1\x30\x00\x00"
_LAN_PORT = 32108
_PRIVATE = tuple(IPv4Network(network) for network in ("10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16"))
_LIMITS = {0: 64 * 1024, 2: 4 * 1024 * 1024}


class CS2Error(Exception):
    """CS2 could not open or carry this session."""


def validate_lan(camera: IPv4Address, source: IPv4Address, lan: IPv4Network):
    if (
        not any(lan.subnet_of(network) for network in _PRIVATE)
        or camera == source
        or any(address not in lan or address in (lan.network_address, lan.broadcast_address)
               for address in (camera, source))
    ):
        raise CS2Error("camera and source must be distinct hosts on the selected private LAN")


def _frame(kind: int, body: bytes = b"") -> bytes:
    return _FRAME.pack(0xF1, kind, len(body)) + body


def _parse_frame(packet: bytes) -> tuple[int, bytes]:
    if len(packet) < _FRAME.size:
        raise CS2Error("truncated CS2 header")
    magic, kind, size = _FRAME.unpack_from(packet)
    if magic != 0xF1 or size != len(packet) - _FRAME.size:
        raise CS2Error("invalid CS2 framing")
    return kind, packet[_FRAME.size:]


async def _exchange(sock, request, destinations, camera, accepted, timeout):
    loop = asyncio.get_running_loop()
    async with asyncio.timeout(timeout):
        while True:
            for destination in destinations:
                await loop.sock_sendto(sock, request, destination)
            retry_at = loop.time() + 1
            try:
                async with asyncio.timeout_at(retry_at):
                    while True:
                        packet, peer = await loop.sock_recvfrom(sock, 65535)
                        if peer[0] != camera:
                            continue
                        try:
                            kind, _ = _parse_frame(packet)
                        except CS2Error:
                            continue
                        if kind in accepted:
                            return kind, packet, peer
            except TimeoutError:
                continue


async def _negotiate(sock, camera, broadcast, timeout, *, port=_LAN_PORT):
    destinations = [(camera, port)]
    if broadcast is not None:
        destinations.append((broadcast, port))
    _, punch, peer = await _exchange(sock, _LAN_SEARCH, destinations, camera, {0x41}, timeout)
    ready, _, peer = await _exchange(sock, punch, [peer], camera, {0x42, 0x43}, timeout)
    if ready != 0x43:
        raise CS2Error("camera nominated CS2/UDP")
    return peer


class TCPTransport:
    """One reader consumes command and media messages in wire order."""

    def __init__(self, reader, writer):
        self._reader = reader
        self._writer = writer
        self._pending = deque()
        self._buffers = {channel: bytearray() for channel in _LIMITS}
        self._sequence = 0
        self._keepalive = asyncio.create_task(self._ping_periodically())

    @classmethod
    async def open(cls, camera: IPv4Address, source: IPv4Address, lan: IPv4Network, *, timeout=10):
        validate_lan(camera, source, lan)
        try:
            async with asyncio.timeout(timeout):
                with socket.socket(socket.AF_INET, socket.SOCK_DGRAM) as sock:
                    sock.setblocking(False)
                    sock.setsockopt(socket.SOL_SOCKET, socket.SO_BROADCAST, 1)
                    sock.bind((str(source), 0))
                    host, port = await _negotiate(sock, str(camera), str(lan.broadcast_address), timeout)
                reader, writer = await asyncio.open_connection(host, port, local_addr=(str(source), 0))
        except TimeoutError:
            raise CS2Error("CS2 handshake timed out") from None
        except OSError:
            raise CS2Error("CS2 handshake socket failed") from None
        return cls(reader, writer)

    async def __aenter__(self):
        return self

    async def __aexit__(self, *_):
        self._keepalive.cancel()
        self._writer.close()
        await asyncio.gather(self._keepalive, return_exceptions=True)
        try:
            async with asyncio.timeout(1):
                await self._writer.wait_closed()
        except (TimeoutError, OSError):
            self._writer.transport.abort()

    async def _write(self, packet):
        self._writer.write(_WRAPPER.pack(len(packet), 0x68) + packet)
        await self._writer.drain()

    async def _ping_periodically(self):
        try:
            while True:
                await asyncio.sleep(1)
                await self._write(_frame(0xE0))
        except OSError:
            self._writer.close()

    async def send_command(self, command: int, payload: bytes):
        message = _SIZE.pack(command) + payload
        if len(message) > 65535 - 12:
            raise CS2Error("CS2 command is too large")
        packet = _frame(0xD0, _DATA.pack(0xD1, 0, self._sequence) + _SIZE.pack(len(message)) + message)
        self._sequence = (self._sequence + 1) & 0xFFFF
        await self._write(packet)

    def _accept_data(self, body):
        if len(body) < _DATA.size:
            raise CS2Error("truncated CS2 data header")
        magic, channel, _ = _DATA.unpack_from(body)
        if magic != 0xD1:
            raise CS2Error("invalid CS2 data magic")
        if channel not in self._buffers:
            return
        buffer = self._buffers[channel]
        buffer.extend(body[_DATA.size:])
        consumed = 0
        while len(buffer) - consumed >= _SIZE.size:
            size, = _SIZE.unpack_from(buffer, consumed)
            if not 0 < size <= _LIMITS[channel]:
                raise CS2Error("invalid CS2 message size")
            end = consumed + _SIZE.size + size
            if end > len(buffer):
                break
            self._pending.append((channel, bytes(buffer[consumed + _SIZE.size:end])))
            consumed = end
        del buffer[:consumed]

    async def receive(self) -> tuple[int, bytes]:
        while not self._pending:
            try:
                size, magic = _WRAPPER.unpack(await self._reader.readexactly(_WRAPPER.size))
                if magic != 0x68 or size < _FRAME.size:
                    raise CS2Error("invalid CS2/TCP wrapper")
                kind, body = _parse_frame(await self._reader.readexactly(size))
            except asyncio.IncompleteReadError:
                raise CS2Error("camera closed CS2/TCP") from None
            if kind == 0xD0:
                self._accept_data(body)
            elif kind == 0xE0:
                await self._write(_frame(0xE1))
            # The reference continues after CS2 close notifications and
            # treats socket EOF or the media deadline as the end of the feed.
        return self._pending.popleft()


class UDPTransport:
    """CS2 datagram transport with bounded per-channel reorder and stream buffers."""

    def __init__(self, sock, peer):
        self._sock, self._peer = sock, peer
        self._pending = deque()
        self._buffers = {channel: bytearray() for channel in _LIMITS}
        self._expected = {0: 0, 2: 0}
        self._reorder = {0: {}, 2: {}}
        self._sequence = {0: 0, 2: 0}
        self._socket_reader = asyncio.Lock()
        self._closed = False

    async def __aenter__(self):
        return self

    async def __aexit__(self, *_):
        self._closed = True
        self._sock.close()

    async def _send_packet(self, packet):
        await asyncio.get_running_loop().sock_sendto(self._sock, packet, self._peer)

    async def send_command(self, command: int, payload: bytes):
        async with self._socket_reader:
            if self._closed:
                raise CS2Error("CS2 session is closed")
            try:
                await self._send_command(command, payload)
            except (CS2Error, OSError, TimeoutError, asyncio.CancelledError):
                # Without an ACK the peer may already have consumed the sequence.
                # Retire this session instead of reusing it for a different command.
                self._closed = True
                self._sock.close()
                raise

    async def _send_command(self, command: int, payload: bytes):
        message = _SIZE.pack(command) + payload
        if len(message) > 65535 - 12:
            raise CS2Error("CS2 command is too large")
        seq = self._sequence[0]
        body = _DATA.pack(0xD1, 0, seq) + _SIZE.pack(len(message)) + message
        packet = _frame(0xD0, body)
        async with asyncio.timeout(6):
            for _ in range(5):
                await self._send_packet(packet)
                try:
                    async with asyncio.timeout(1):
                        while True:
                            datagram, peer = await asyncio.get_running_loop().sock_recvfrom(self._sock, 65535)
                            if peer[0] != self._peer[0]:
                                continue
                            kind, ack = _parse_frame(datagram)
                            if kind == 0xD1 and len(ack) >= 6:
                                count = int.from_bytes(ack[2:4], "big")
                                if (
                                    ack[:2] == b"\xd1\x00" and len(ack) == 4 + 2 * count
                                    and any(ack[index:index + 2] == seq.to_bytes(2, "big")
                                            for index in range(4, len(ack), 2))
                                ):
                                    self._sequence[0] = (seq + 1) & 0xFFFF
                                    return
                            await self._handle_packet(datagram)
                except TimeoutError:
                    continue
        raise CS2Error("CS2 command acknowledgement timed out")

    def _ingest(self, packet):
        kind, body = _parse_frame(packet)
        if kind == 0xD0:
            if len(body) < _DATA.size:
                raise CS2Error("truncated CS2 data packet")
            magic, channel, seq = _DATA.unpack_from(body)
            if magic != 0xD1:
                raise CS2Error("invalid CS2 data magic")
            if channel not in self._buffers:
                return channel, seq
            expected, queued = self._expected[channel], self._reorder[channel]
            diff = (seq - expected) & 0xFFFF
            if diff >= 0x8000:
                return channel, seq
            if diff:
                if diff > 250 or len(queued) >= 250:
                    return
                queued.setdefault(seq, body[4:])
                return channel, seq
            self._push(channel, body[4:])
            self._expected[channel] = (expected + 1) & 0xFFFF
            while self._expected[channel] in queued:
                data = queued.pop(self._expected[channel])
                self._push(channel, data)
                self._expected[channel] = (self._expected[channel] + 1) & 0xFFFF
            return channel, seq
        elif kind == 0xE0:
            return

    def _push(self, channel, data):
        buf = self._buffers[channel]
        buf.extend(data)
        while len(buf) >= 4:
            size, = _SIZE.unpack_from(buf)
            if not 0 < size <= _LIMITS[channel]:
                raise CS2Error("invalid CS2 message size")
            if len(buf) < 4 + size:
                break
            if len(self._pending) >= 128:
                raise CS2Error("CS2 receive queue is full")
            self._pending.append((channel, bytes(buf[4:4 + size])))
            del buf[:4 + size]

    async def _handle_packet(self, packet):
        kind, _ = _parse_frame(packet)
        if kind == 0xE0:
            await self._send_packet(_frame(0xE1))
        elif (accepted := self._ingest(packet)) is not None:
            channel, seq = accepted
            await self._send_packet(_frame(0xD1, bytes((0xD1, channel, 0, 1)) + seq.to_bytes(2, "big")))

    async def receive(self):
        while not self._pending:
            async with self._socket_reader:
                if self._pending:
                    break
                if self._closed:
                    raise CS2Error("CS2 session is closed")
                packet, peer = await asyncio.get_running_loop().sock_recvfrom(self._sock, 65535)
                if peer[0] == self._peer[0]:
                    await self._handle_packet(packet)
        return self._pending.popleft()


async def open_transport(camera, source, lan, *, timeout=10, preference="auto"):
    """Open nominated CS2 transport; preference can force TCP or UDP."""
    if preference not in ("auto", "tcp", "udp"):
        raise CS2Error("transport preference must be auto, tcp, or udp")
    validate_lan(camera, source, lan)
    try:
        async with asyncio.timeout(timeout):
            sock = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
            try:
                sock.setblocking(False)
                sock.setsockopt(socket.SOL_SOCKET, socket.SO_BROADCAST, 1)
                sock.bind((str(source), 0))
                destinations = [(str(camera), _LAN_PORT), (str(lan.broadcast_address), _LAN_PORT)]
                _, punch, peer = await _exchange(sock, _LAN_SEARCH, destinations, str(camera), {0x41}, timeout)
                accepted = {"auto": {0x42, 0x43}, "udp": {0x42}, "tcp": {0x43}}[preference]
                ready, _, peer = await _exchange(sock, punch, [peer], str(camera), accepted, timeout)
                selected = "udp" if ready == 0x42 else "tcp"
                if selected == "udp":
                    return UDPTransport(sock, peer)
                host, port = peer[0], peer[1]
            except BaseException:
                sock.close()
                raise
            sock.close()
            reader, writer = await asyncio.open_connection(host, port, local_addr=(str(source), 0))
            return TCPTransport(reader, writer)
    except TimeoutError:
        raise CS2Error("CS2 handshake timed out") from None
    except OSError:
        raise CS2Error("CS2 handshake socket failed") from None
