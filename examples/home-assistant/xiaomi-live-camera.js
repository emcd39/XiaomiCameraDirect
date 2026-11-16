const CHECK_INTERVAL = 5_000;
const STARTUP_GRACE = 25_000;
const MAX_RETRY_DELAY = 60_000;

function findVideo(root) {
  if (root.localName === "video") return root;
  if (root.shadowRoot) {
    const video = findVideo(root.shadowRoot);
    if (video) return video;
  }
  for (const child of root.children) {
    const video = findVideo(child);
    if (video) return video;
  }
  return null;
}

class XiaomiLiveCamera extends HTMLElement {
  constructor() {
    super();
    this.container = this.attachShadow({ mode: "open" });
    const style = document.createElement("style");
    style.textContent = ":host { display: block; }";
    this.content = document.createElement("div");
    this.container.append(style, this.content);
    this.resume = () => this.resetWatch(performance.now());
  }

  setConfig(config) {
    if (!config.camera_image || config.camera_view !== "live") {
      throw new Error("Xiaomi Live Camera requires camera_image and camera_view: live");
    }
    this.config = { ...config, type: "picture-elements" };
    this.retryDelay = STARTUP_GRACE;
    if (this.helpers) this.mountCard();
  }

  set hass(hass) {
    this.hassState = hass;
    if (this.card) this.card.hass = hass;
  }

  connectedCallback() {
    if (!this.card) {
      this.helpersPromise ??= window.loadCardHelpers();
      void this.helpersPromise.then((helpers) => {
        this.helpers = helpers;
        if (this.isConnected && this.config && !this.card) this.mountCard();
      });
    }
    this.resetWatch(performance.now());
    document.addEventListener("visibilitychange", this.resume);
    this.timer = setInterval(() => this.checkPlayback(), CHECK_INTERVAL);
  }

  disconnectedCallback() {
    clearInterval(this.timer);
    document.removeEventListener("visibilitychange", this.resume);
  }

  getCardSize() {
    return this.card?.getCardSize?.() ?? 3;
  }

  mountCard() {
    const card = this.helpers.createCardElement(this.config);
    this.card = card;
    if (this.hassState) card.hass = this.hassState;
    this.content.replaceChildren(card);
    this.resetWatch(performance.now());
  }

  resetWatch(now) {
    this.lastProgress = now;
    this.lastVideo = null;
    this.lastTime = 0;
  }

  checkPlayback() {
    if (document.hidden || !this.card) return;
    const now = performance.now();
    const state = this.hassState?.states[this.config.camera_image]?.state;
    if (!state || state === "unavailable" || state === "unknown" || state === "off") {
      this.resetWatch(now);
      return;
    }
    const video = findVideo(this.card);
    if (video && !video.error) {
      if (video === this.lastVideo && video.currentTime !== this.lastTime) {
        this.lastProgress = now;
        this.retryDelay = STARTUP_GRACE;
      }
      this.lastTime = video.currentTime;
    }
    this.lastVideo = video;

    if (now - this.lastProgress >= this.retryDelay) {
      this.retryDelay = Math.min(this.retryDelay * 2, MAX_RETRY_DELAY);
      this.mountCard();
    }
  }
}

if (!customElements.get("xiaomi-live-camera")) {
  customElements.define("xiaomi-live-camera", XiaomiLiveCamera);
}
