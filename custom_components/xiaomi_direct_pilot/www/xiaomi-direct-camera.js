const HLS_CONFIG = {
  // Low-latency HLS, held to a fixed sync distance instead of the player's
  // default. The card ships hls.js because the frontend does not expose it.
  lowLatencyMode: true,
  backBufferLength: 0,
  liveSyncDuration: 0.9,
  liveMaxLatencyDuration: 4,
  maxLiveSyncPlaybackRate: 1.3,
  enableWorker: true,
};
const HLS_SCRIPT = "/xiaomi-direct/hls.light.min.js";
const PTZ_HOLD_DELAY = 520;
const PTZ_HOLD_INTERVAL = 260;

class XiaomiDirectCameraCard extends HTMLElement {
  constructor() {
    super();
    this._hass = undefined;
    this._config = undefined;
    this._hls = undefined;
    this._hlsLoader = undefined;
    this._starting = false;
    this._ptzEnabled = true;
    this._showLatency = true;
    this._ptzButtons = {};
    this._ptzBusy = false;
    this._ptzHold = undefined;
    this._ptzRepeat = undefined;
    this._ptzErrorTimer = undefined;
    this._latencyTimer = undefined;
    this._retryTimer = undefined;

    const root = this.attachShadow({ mode: "open" });
    root.innerHTML = `
      <style>
        :host { display: block; }
        .stage { position: relative; background: #000; overflow: hidden;
          border-radius: var(--ha-card-border-radius, 12px); }
        video { display: block; width: 100%; aspect-ratio: 16 / 9; object-fit: contain;
          background: #000; cursor: pointer; }
        .latency { position: absolute; right: 8px; top: 8px; padding: 2px 6px;
          border-radius: 6px; background: rgba(0, 0, 0, .55); color: #fff; font-size: 11px;
          font-variant-numeric: tabular-nums; }
        .latency[hidden] { display: none; }
        .overlay { position: absolute; inset: 0; display: flex; align-items: center;
          justify-content: center; padding: 16px; text-align: center; color: #fff;
          font-size: 13px; background: rgba(0, 0, 0, .62); }
        .overlay[hidden] { display: none; }
        .ptz { position: absolute; left: 10px; bottom: 10px; display: grid;
          grid-template-columns: repeat(3, 34px); grid-template-rows: repeat(3, 34px);
          gap: 3px; padding: 6px; border-radius: 14px;
          background: rgba(0, 0, 0, .42); backdrop-filter: blur(3px); }
        .ptz[hidden] { display: none; }
        .ptz button { display: inline-flex; align-items: center; justify-content: center;
          width: 34px; height: 34px; padding: 0; border-radius: 50%;
          border: 1px solid rgba(255, 255, 255, .38); background: rgba(255, 255, 255, .14);
          color: #fff; cursor: pointer; touch-action: none; }
        .ptz button[data-active="true"] { background: var(--primary-color);
          border-color: var(--primary-color); }
        .ptz[data-live="false"] button { opacity: .55; }
        .ptz-up { grid-area: 1 / 2; }
        .ptz-left { grid-area: 2 / 1; }
        .ptz-right { grid-area: 2 / 3; }
        .ptz-down { grid-area: 3 / 2; }
        .ptz-error { position: absolute; left: 0; bottom: calc(100% + 4px); max-width: 260px;
          padding: 4px 8px; border-radius: 8px; background: rgba(0, 0, 0, .75); color: #fff;
          font-size: 12px; line-height: 1.3; }
        .ptz-error:empty { display: none; }
      </style>
      <div class="stage">
        <video playsinline muted autoplay></video>
        <span class="latency" hidden></span>
        <div class="overlay" hidden>正在连接…</div>
        <div class="ptz" hidden>
          <button type="button" class="ptz-up" aria-label="Nahoru"><ha-icon icon="mdi:chevron-up"></ha-icon></button>
          <button type="button" class="ptz-left" aria-label="Vlevo"><ha-icon icon="mdi:chevron-left"></ha-icon></button>
          <button type="button" class="ptz-right" aria-label="Vpravo"><ha-icon icon="mdi:chevron-right"></ha-icon></button>
          <button type="button" class="ptz-down" aria-label="Dolů"><ha-icon icon="mdi:chevron-down"></ha-icon></button>
          <span class="ptz-error" role="status" aria-live="polite"></span>
        </div>
      </div>`;

    this._video = root.querySelector("video");
    this._latency = root.querySelector(".latency");
    this._overlay = root.querySelector(".overlay");
    this._ptz = root.querySelector(".ptz");
    this._ptzError = root.querySelector(".ptz-error");
    for (const direction of ["up", "down", "left", "right"]) {
      const button = root.querySelector(`.ptz-${direction}`);
      button.addEventListener("pointerdown", (event) => this._beginPtz(direction, event));
      for (const type of ["pointerup", "pointercancel", "pointerleave", "lostpointercapture"]) {
        button.addEventListener(type, () => this._endPtz());
      }
      button.addEventListener("keydown", (event) => {
        if (event.code !== "Space" && event.code !== "Enter") return;
        event.preventDefault();
        if (!event.repeat) this._stepPtz(direction);
      });
      this._ptzButtons[direction] = button;
    }
    this._video.addEventListener("click", () => {
      this._video.muted = !this._video.muted;
    });
    this._visibility = () => {
      if (document.hidden) this._endPtz();
    };
  }

  connectedCallback() {
    document.addEventListener("visibilitychange", this._visibility);
    this._maybeStart();
    this._tickLatency();
  }

  disconnectedCallback() {
    document.removeEventListener("visibilitychange", this._visibility);
    this._endPtz();
    this._teardownHls();
    clearInterval(this._latencyTimer);
    clearTimeout(this._retryTimer);
  }

  setConfig(config) {
    if (!config.entity) throw new Error("Xiaomi Direct card requires an entity");
    this._config = { ...config };
    this._ptzEnabled = config.ptz !== false;
    this._showLatency = config.latency_indicator !== false;
    if (!this._ptzEnabled) {
      this._ptz.hidden = true;
      this._endPtz();
    }
    this._maybeStart();
    this._tickLatency();
  }

  set hass(hass) {
    this._hass = hass;
    this._maybeStart();
    this._syncPtz(hass.states?.[this._config?.entity]);
  }

  getCardSize() {
    return 5;
  }

  _maybeStart(force = false) {
    if (this._hls && !force) return;
    if (force) this._teardownHls();
    if (!this._hass || !this._config || this._starting) return;
    this._start();
  }

  async _start() {
    this._starting = true;
    this._overlay.textContent = "正在连接…";
    this._overlay.hidden = false;
    try {
      const { url } = await this._hass.callWS({
        type: "camera/stream",
        entity_id: this._config.entity,
      });
      const Hls = await this._ensureHls();
      if (!this.isConnected) return;
      const hls = new Hls(HLS_CONFIG);
      hls.on(Hls.Events.ERROR, (_event, data) => this._onHlsError(Hls, data));
      hls.on(Hls.Events.FRAG_BUFFERED, () => { this._overlay.hidden = true; });
      hls.loadSource(new URL(url, window.location.origin).toString());
      hls.attachMedia(this._video);
      this._hls = hls;
      const playback = this._video.play?.();
      playback?.catch?.(() => { /* the browser may wait for a tap */ });
    } catch (error) {
      this._fail(error);
    } finally {
      this._starting = false;
    }
  }

  async _ensureHls() {
    if (window.Hls) return window.Hls;
    if (!this._hlsLoader) {
      this._hlsLoader = new Promise((resolve, reject) => {
        const script = document.createElement("script");
        script.src = HLS_SCRIPT;
        script.async = true;
        script.onload = () => (window.Hls ? resolve(window.Hls) : reject(new Error("hls.js 未初始化")));
        script.onerror = () => reject(new Error("无法加载 hls.js"));
        document.head.append(script);
      }).catch((error) => {
        this._hlsLoader = undefined;
        throw error;
      });
    }
    return this._hlsLoader;
  }

  _onHlsError(Hls, data) {
    if (!data.fatal) return;
    if (data.type === Hls.ErrorTypes.NETWORK_ERROR) {
      this._hls?.startLoad();
      return;
    }
    if (data.type === Hls.ErrorTypes.MEDIA_ERROR) {
      this._hls?.recoverMediaError();
      return;
    }
    this._fail(new Error(data.details || "视频流中断"));
  }

  _fail(error) {
    this._overlay.textContent = error?.message || "视频流无法播放";
    this._overlay.hidden = false;
    this._teardownHls();
    clearTimeout(this._retryTimer);
    this._retryTimer = setTimeout(() => this._maybeStart(true), 6000);
  }

  _teardownHls() {
    if (this._hls) {
      this._hls.destroy();
      this._hls = undefined;
    }
  }

  _tickLatency() {
    clearInterval(this._latencyTimer);
    if (!this._showLatency) {
      this._latency.hidden = true;
      return;
    }
    this._latencyTimer = setInterval(() => {
      const video = this._video;
      if (!video?.buffered?.length) {
        this._latency.hidden = true;
        return;
      }
      const live = video.buffered.end(video.buffered.length - 1) - video.currentTime;
      this._latency.textContent = `${live.toFixed(1)} s`;
      this._latency.hidden = false;
    }, 1000);
  }

  _syncPtz(state) {
    const attributes = state?.attributes || {};
    const directions = Array.isArray(attributes.ptz_directions) ? attributes.ptz_directions : [];
    const visible = this._ptzEnabled && directions.length > 0 && state?.state !== "unavailable";
    if (this._ptz.hidden !== !visible) this._ptz.hidden = !visible;
    this._ptz.setAttribute("data-live", attributes.ptz_enabled === true ? "true" : "false");
    if (!visible) this._endPtz();
  }

  _beginPtz(direction, event) {
    event?.preventDefault?.();
    if (this._ptz.hidden || !this._hass) return;
    try { event?.currentTarget?.setPointerCapture?.(event.pointerId); } catch (_) { /* optional */ }
    this._endPtz();
    this._ptzButtons[direction]?.setAttribute("data-active", "true");
    this._stepPtz(direction);
    this._ptzHold = setTimeout(() => {
      this._ptzRepeat = setInterval(() => this._stepPtz(direction), PTZ_HOLD_INTERVAL);
    }, PTZ_HOLD_DELAY);
  }

  _endPtz() {
    if (this._ptzHold) clearTimeout(this._ptzHold);
    if (this._ptzRepeat) clearInterval(this._ptzRepeat);
    this._ptzHold = undefined;
    this._ptzRepeat = undefined;
    for (const button of Object.values(this._ptzButtons)) {
      button?.setAttribute("data-active", "false");
    }
  }

  async _stepPtz(direction) {
    if (this._ptzBusy || !this._hass || !this._config) return;
    this._ptzBusy = true;
    try {
      await this._hass.callWS({
        type: "xiaomi_direct_pilot/ptz/step",
        entity_id: this._config.entity,
        direction,
      });
      this._showPtzError("");
    } catch (error) {
      this._showPtzError(error?.message || "Pohyb kamery se nepodařil");
      this._endPtz();
    } finally {
      this._ptzBusy = false;
    }
  }

  _showPtzError(message) {
    if (this._ptzErrorTimer) clearTimeout(this._ptzErrorTimer);
    this._ptzErrorTimer = undefined;
    this._ptzError.textContent = message;
    if (message) {
      this._ptzErrorTimer = setTimeout(() => { this._ptzError.textContent = ""; }, 4000);
    }
  }
}

if (!customElements.get("xiaomi-direct-camera")) {
  customElements.define("xiaomi-direct-camera", XiaomiDirectCameraCard);
}

window.customCards = window.customCards || [];
window.customCards.push({
  type: "xiaomi-direct-camera",
  name: "Xiaomi Direct camera",
  description: "Live camera with a PTZ pad, streaming over low-latency HLS.",
  preview: false,
});
