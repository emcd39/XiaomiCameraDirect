"""Convert MISS microphone packets into an AAC track for HA HLS."""

from fractions import Fraction

import av


_DECODERS = {"pcma": "pcm_alaw", "pcmu": "pcm_mulaw", "opus": "opus"}
_OUTPUT_RATE = 16000
_CLOCK = Fraction(1, _OUTPUT_RATE)


def supports_audio(packet):
    return packet.codec in _DECODERS and 0 < len(packet.payload) <= packet.sample_rate


class AACAudio:
    """Encode mono audio on the same clock used by the incoming video."""

    def __init__(self, container, packet, origin):
        self._format = (packet.codec, packet.sample_rate)
        self._decoder = av.CodecContext.create(_DECODERS[packet.codec], "r")
        self._decoder.sample_rate = packet.sample_rate
        self._decoder.layout = "stereo" if packet.codec == "opus" else "mono"
        self._resampler = av.AudioResampler(format="fltp", layout="mono", rate=_OUTPUT_RATE)
        self._stream = container.add_stream("aac", rate=_OUTPUT_RATE)
        self._stream.layout = "mono"
        self._stream.bit_rate = 32000
        self._origin = origin
        self._next_pts = 0
        self._offset = None

    def encode(self, packet):
        if (packet.codec, packet.sample_rate) != self._format or not supports_audio(packet):
            return
        if packet.timestamp < self._origin:
            return
        source_pts = (packet.timestamp - self._origin) * _OUTPUT_RATE // 1000
        # Keep the encoder's sample clock continuous and rebase output after
        # a camera interruption, when the shared stream skips ahead in time.
        if self._offset is None or source_pts > self._next_pts + self._offset + _OUTPUT_RATE // 4:
            self._offset = source_pts - self._next_pts
        for decoded in self._decoder.decode(av.Packet(packet.payload)):
            for frame in self._resampler.resample(decoded):
                frame.time_base = _CLOCK
                frame.pts = self._next_pts
                self._next_pts += frame.samples
                for encoded in self._stream.encode(frame):
                    if encoded.pts is not None and encoded.pts >= 0 and encoded.dts is not None:
                        encoded.pts += self._offset
                        encoded.dts += self._offset
                        yield encoded
