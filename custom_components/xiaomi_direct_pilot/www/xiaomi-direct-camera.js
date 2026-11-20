class XiaomiDirectCameraCard extends HTMLElement {
  constructor() {
    super();
    this._hass = undefined;
    this._config = undefined;
    this._cameraConfig = undefined;
    this._cameraReady = undefined;
    this._volumeVideo = undefined;
    this._volumeSyncHandler = () => this._syncVolume();
    this._pressed = false;
    this._sendQueue = Promise.resolve();
    this._encodedFrames = [];
    this._pendingBatches = 0;
    this._closed = false;

    const root = this.attachShadow({ mode: "open" });
    root.innerHTML = `
      <style>
        :host { display: block; }
        .controls { display: flex; align-items: center; justify-content: space-between;
          flex-wrap: wrap; gap: 8px 16px; padding: 8px 12px;
          background: var(--card-background-color); color: var(--primary-text-color); }
        label, .talkback { display: flex; align-items: center; flex-wrap: wrap; gap: 8px; }
        input { width: 88px; accent-color: var(--primary-color); }
        button { border: 1px solid var(--divider-color); border-radius: 8px; padding: 6px 10px;
          background: transparent; color: inherit; font: inherit; touch-action: none; user-select: none; }
        button:disabled { opacity: .5; }
        button[aria-pressed="true"] { background: var(--error-color); color: white; }
        .status { color: var(--secondary-text-color); font-size: 12px; }
      </style>
      <div class="video"></div>
      <div class="controls">
        <label>Hlasitost poslechu <input type="range" min="0" max="100" value="100" disabled></label>
        <div class="talkback">
          <button type="button" aria-pressed="false" disabled>🎙 Podrž pro mluvení</button>
          <span class="status" aria-live="polite"></span>
        </div>
      </div>`;
    this._camera = undefined;
    this._videoHost = root.querySelector(".video");
    this._volumeSlider = root.querySelector("input");
    this._button = root.querySelector(".talkback button");
    this._status = root.querySelector(".status");
    this._volumeSlider.addEventListener("input", () => {
      if (this._volumeVideo) this._volumeVideo.volume = this._volumeSlider.valueAsNumber / 100;
    });
    this._button.addEventListener("pointerdown", (event) => this._press(event));
    this._button.addEventListener("pointerup", () => this._release());
    this._button.addEventListener("pointercancel", () => this._release());
    this._button.addEventListener("lostpointercapture", () => this._release());
    this._button.addEventListener("keydown", (event) => {
      if ((event.code === "Space" || event.code === "Enter") && !event.repeat) {
        event.preventDefault();
        this._beginTalk();
      }
    });
    this._button.addEventListener("keyup", (event) => {
      if (event.code === "Space" || event.code === "Enter") this._release();
    });
    this._visibility = () => {
      if (document.hidden) this._release();
    };
  }

  connectedCallback() {
    document.addEventListener("visibilitychange", this._visibility);
    if (this._camera) this._bindVolumeIcon();
  }

  setConfig(config) {
    if (!config.entity) throw new Error("Xiaomi Direct card requires an entity");
    this._config = { ...config };
    const cameraConfig = { ...config };
    delete cameraConfig.type;
    delete cameraConfig.push_to_talk;
    this._cameraConfig = cameraConfig;

    // Do not attach <webrtc-camera> until it is defined and configured.
    // Its connectedCallback reads config.muted immediately, so inserting it
    // from this card's constructor causes a frontend exception.
    this._cameraReady = customElements.whenDefined("webrtc-camera").then(() => {
      if (typeof customElements.get("webrtc-camera") !== "function") {
        throw new Error("WebRTC Camera card se nenačetla správně");
      }
      if (!this._camera) {
        const camera = document.createElement("webrtc-camera");
        camera.setConfig(this._cameraConfig);
        if (this._hass) camera.hass = this._hass;
        this._camera = camera;
        this._videoHost.append(camera);
        this._bindVolumeIcon();
      } else {
        this._camera.setConfig(this._cameraConfig);
        if (this._hass) this._camera.hass = this._hass;
        this._bindVolumeIcon();
      }
    }).catch((error) => {
      this._status.textContent = error?.message || "WebRTC Camera card se nepodařilo načíst";
    });
  }

  set hass(hass) {
    this._hass = hass;
    if (this._camera) this._camera.hass = hass;
    const state = this._config && hass.states[this._config.entity];
    const enabled = state?.state !== "unavailable" && state?.attributes?.talkback_enabled === true;
    this._button.disabled = !enabled;
    this._status.textContent = enabled ? "Mikrofon C701 je zapnutý v integraci" : "Zapni mikrofon C701 v nastavení integrace";
  }

  _bindVolumeIcon() {
    const video = this._camera?.video || this._camera?.shadowRoot?.querySelector("video");
    if (video && video !== this._volumeVideo) {
      this._volumeVideo?.removeEventListener("loadeddata", this._volumeSyncHandler);
      this._volumeVideo?.removeEventListener("volumechange", this._volumeSyncHandler);
      this._volumeVideo = video;
      video.addEventListener("loadeddata", this._volumeSyncHandler);
      video.addEventListener("volumechange", this._volumeSyncHandler);
    }
    this._syncVolume();
  }

  _syncVolume() {
    const video = this._volumeVideo;
    const volume = this._camera?.shadowRoot?.querySelector(".volume");
    if (!video) return;
    this._volumeSlider.disabled = !this._camera.hasAudio;
    this._volumeSlider.value = Math.round(video.volume * 100);
    // WebRTC Camera renders its icon after applying the initial muted setting.
    if (volume) volume.icon = video.muted ? "mdi:volume-mute" : "mdi:volume-high";
  }

  getCardSize() { return 6; }

  disconnectedCallback() {
    this._release();
    document.removeEventListener("visibilitychange", this._visibility);
    this._volumeVideo?.removeEventListener("loadeddata", this._volumeSyncHandler);
    this._volumeVideo?.removeEventListener("volumechange", this._volumeSyncHandler);
    this._volumeVideo = undefined;
  }

  _press(event) {
    event.preventDefault();
    try { this._button.setPointerCapture(event.pointerId); } catch (_) { /* capture is optional */ }
    this._beginTalk();
  }

  _beginTalk() {
    if (this._pressed || this._button.disabled || !this._hass) return;
    this._pressed = true;
    this._button.setAttribute("aria-pressed", "true");
    this._status.textContent = "Připojuji mikrofon…";
    this._startPromise = this._startTalkback();
  }

  async _startTalkback() {
    let session;
    try {
      if (!window.isSecureContext) throw new Error("Mikrofon prohlížeče vyžaduje HTTPS nebo localhost");
      if (!navigator.mediaDevices?.getUserMedia || typeof AudioEncoder === "undefined" ||
          typeof MediaStreamTrackProcessor === "undefined") {
        throw new Error("Tento prohlížeč neumí otevřít Opus talkback");
      }
      const config = {
        codec: "opus", sampleRate: 48000, numberOfChannels: 1,
        bitrate: 24000, opus: { application: "voip" },
      };
      const support = await AudioEncoder.isConfigSupported(config);
      if (!support.supported) throw new Error("Prohlížeč nemá Opus encoder pro C701");

      this._stream = await navigator.mediaDevices.getUserMedia({
        audio: {
          channelCount: { ideal: 1 }, sampleRate: { ideal: 48000 },
          echoCancellation: true, noiseSuppression: true, autoGainControl: true,
        },
      });
      const track = this._stream.getAudioTracks()[0];
      const settings = track?.getSettings?.() || {};
      if (!track || (settings.sampleRate && settings.sampleRate !== 48000) ||
          (settings.channelCount && settings.channelCount !== 1)) {
        throw new Error("Mikrofon musí poskytovat mono zvuk 48 kHz");
      }

      session = await this._hass.callWS({
        type: "xiaomi_direct_pilot/talkback/start",
        entity_id: this._config.entity,
      });
      this._sessionId = session.session_id;
      this._encoder = new AudioEncoder({
        output: (chunk) => this._onEncoded(chunk),
        error: (error) => this._fail(error),
      });
      this._encoder.configure(config);
      const processor = new MediaStreamTrackProcessor({ track });
      this._reader = processor.readable.getReader();
      this._captureTask = this._captureAudio();
      this._status.textContent = "Mluvíš — pusť tlačítko pro ukončení";
    } catch (error) {
      this._status.textContent = error?.message || "Mikrofon se nepodařilo spustit";
      this._pressed = false;
      this._button.setAttribute("aria-pressed", "false");
      await this._closeAudio();
      if (session?.session_id) await this._stopSession(session.session_id);
    }
  }

  async _captureAudio() {
    let pending = new Float32Array(0);
    let timestamp = Math.round(performance.now() * 1000);
    try {
      while (this._pressed && this._reader) {
        const { value, done } = await this._reader.read();
        if (done) break;
        try {
          if (value.sampleRate !== 48000 || value.numberOfChannels !== 1) {
            throw new Error("Mikrofon musí poskytovat mono zvuk 48 kHz");
          }
          const samples = new Float32Array(value.numberOfFrames);
          value.copyTo(samples, { planeIndex: 0, format: "f32-planar" });
          const combined = new Float32Array(pending.length + samples.length);
          combined.set(pending);
          combined.set(samples, pending.length);
          let offset = 0;
          while (combined.length - offset >= 960 && this._pressed && this._encoder?.state === "configured") {
            const frame = combined.slice(offset, offset + 960);
            const audio = new AudioData({
              format: "f32-planar", sampleRate: 48000, numberOfFrames: 960,
              numberOfChannels: 1, timestamp, data: frame.buffer,
            });
            this._encoder.encode(audio);
            audio.close();
            offset += 960;
            timestamp += 20000;
          }
          pending = combined.slice(offset);
        } finally {
          value.close();
        }
      }
    } catch (error) {
      if (this._pressed) this._fail(error);
    }
  }

  _onEncoded(chunk) {
    if (!this._sessionId || this._closed) return;
    const bytes = new Uint8Array(chunk.byteLength);
    chunk.copyTo(bytes);
    let binary = "";
    for (const byte of bytes) binary += String.fromCharCode(byte);
    this._encodedFrames.push(btoa(binary));
    if (this._encodedFrames.length >= 2) this._sendFrames(2);
  }

  _sendFrames(count) {
    if (!this._sessionId || !this._encodedFrames.length) return;
    if (this._pendingBatches >= 3) {
      this._fail(new Error("Přenos talkback nestíhá; mikrofon se bezpečně vypíná"));
      return;
    }
    const frames = this._encodedFrames.splice(0, count);
    this._pendingBatches += 1;
    const sessionId = this._sessionId;
    this._sendQueue = this._sendQueue.then(() => this._hass.callWS({
      type: "xiaomi_direct_pilot/talkback/audio", session_id: sessionId, frames,
    })).catch((error) => {
      this._status.textContent = error?.message || "Přenos do reproduktoru C701 selhal";
      this._pressed = false;
      this._button.setAttribute("aria-pressed", "false");
      setTimeout(() => this._release(), 0);
    }).finally(() => { this._pendingBatches -= 1; });
  }

  _fail(error) {
    this._status.textContent = error?.message || "Přenos do reproduktoru C701 selhal";
    this._pressed = false;
    this._button.setAttribute("aria-pressed", "false");
    this._release();
  }

  _release() {
    if (!this._releasePromise) {
      this._releasePromise = this._finishTalkback().finally(() => { this._releasePromise = undefined; });
    }
    return this._releasePromise;
  }

  async _finishTalkback() {
    if (this._closed) return;
    if (!this._pressed && !this._sessionId && !this._stream) return;
    this._pressed = false;
    this._button.setAttribute("aria-pressed", "false");
    const started = this._startPromise;
    if (started) await started;
    const sessionId = this._sessionId;
    try { await this._reader?.cancel(); } catch (_) { /* already stopped */ }
    for (const track of this._stream?.getTracks?.() || []) track.stop();
    try { await this._captureTask; } catch (_) { /* capture was interrupted */ }
    if (this._encoder?.state === "configured") {
      try { await this._encoder.flush(); } catch (_) { /* encoder may have been interrupted */ }
    }
    this._sendFrames(4);
    await this._sendQueue;
    await this._closeAudio();
    if (sessionId) await this._stopSession(sessionId);
    this._status.textContent = "Talkback ukončen";
    this._startPromise = undefined;
  }

  async _closeAudio() {
    this._closed = true;
    try { await this._reader?.cancel(); } catch (_) { /* already stopped */ }
    this._reader = undefined;
    try { this._encoder?.close(); } catch (_) { /* already closed */ }
    this._encoder = undefined;
    for (const track of this._stream?.getTracks?.() || []) track.stop();
    this._stream = undefined;
    this._captureTask = undefined;
    this._closed = false;
  }

  async _stopSession(sessionId) {
    this._sessionId = undefined;
    try {
      await this._hass.callWS({ type: "xiaomi_direct_pilot/talkback/stop", session_id: sessionId });
    } catch (_) { /* HA disconnect cleanup and server deadman also stop the speaker */ }
  }
}

if (!customElements.get("xiaomi-direct-camera")) {
  customElements.define("xiaomi-direct-camera", XiaomiDirectCameraCard);
}

window.customCards = window.customCards || [];
window.customCards.push({ type: "xiaomi-direct-camera", name: "Xiaomi Direct camera with talkback", preview: false });
