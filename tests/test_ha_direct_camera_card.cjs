const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const test = require("node:test");
const vm = require("node:vm");

const source = fs.readFileSync(path.join(__dirname,
  "../custom_components/xiaomi_direct_pilot/www/xiaomi-direct-camera.js"), "utf8");

function makeElement(selector) {
  return {
    selector, children: [], listeners: {}, attrs: {}, dataset: {}, style: {},
    textContent: "", hidden: false, disabled: false,
    addEventListener(type, handler) { (this.listeners[type] ||= []).push(handler); },
    removeEventListener(type, handler) {
      this.listeners[type] = (this.listeners[type] || []).filter((fn) => fn !== handler);
    },
    append(child) { this.children.push(child); },
    setAttribute(name, value) { this.attrs[name] = value; },
    getAttribute(name) { return this.attrs[name]; },
    removeAttribute(name) { delete this.attrs[name]; },
    querySelector() { return undefined; },
    fire(type, event = {}) {
      for (const handler of (this.listeners[type] || []).slice()) handler(event);
    },
  };
}

function makeVideo() {
  const video = makeElement("video");
  video.muted = true;
  video.currentTime = 10.4;
  video.buffered = { length: 1, end: () => 12 };
  video.play = () => Promise.resolve();
  video.canPlayType = () => "";
  return video;
}

class FakeHls {
  constructor(config) {
    this.config = config;
    this.handlers = {};
    this.destroyed = false;
    FakeHls.instances.push(this);
  }
  static isSupported() { return true; }
  static get Events() { return { ERROR: "hlsError", FRAG_BUFFERED: "fragBuffered" }; }
  static get ErrorTypes() {
    return { NETWORK_ERROR: "networkError", MEDIA_ERROR: "mediaError", OTHER_ERROR: "otherError" };
  }
  on(event, handler) { this.handlers[event] = handler; }
  loadSource(url) { this.source = url; }
  attachMedia(media) { this.media = media; }
  startLoad() { this.reloaded = true; }
  recoverMediaError() { this.recovered = true; }
  destroy() { this.destroyed = true; }
  emit(event, data) { this.handlers[event]?.(event, data); }
}
FakeHls.instances = [];

function createClock() {
  let now = 0;
  let nextId = 1;
  const timers = new Map();
  const schedule = (fn, ms, repeat) => {
    const id = nextId++;
    timers.set(id, { fn, at: now + ms, ms, repeat });
    return id;
  };
  return {
    setTimeout: (fn, ms) => schedule(fn, ms, false),
    clearTimeout: (id) => timers.delete(id),
    setInterval: (fn, ms) => schedule(fn, ms, true),
    clearInterval: (id) => timers.delete(id),
    advance(ms) {
      const target = now + ms;
      for (let guard = 0; guard < 500; guard += 1) {
        const due = [...timers.entries()]
          .filter(([, timer]) => timer.at <= target)
          .sort((left, right) => left[1].at - right[1].at)[0];
        if (!due) break;
        const [id, timer] = due;
        now = timer.at;
        if (timer.repeat) timer.at = now + timer.ms;
        else timers.delete(id);
        timer.fn();
      }
      now = target;
    },
    get pending() { return timers.size; },
  };
}

const flush = () => new Promise((resolve) => setImmediate(resolve));

function cameraState({ ptz = true, live = true, state = "streaming" } = {}) {
  return {
    state,
    attributes: {
      ...(ptz ? { ptz_directions: ["left", "right", "up", "down"], ptz_enabled: live } : {}),
    },
  };
}

async function setUpCard(config = {}, { withHls = true } = {}) {
  FakeHls.instances = [];
  const elements = new Map();
  const calls = [];
  const head = { children: [], append(child) { this.children.push(child); } };
  const clock = createClock();
  const window = { customCards: [], location: { origin: "http://ha.local" } };
  if (withHls) window.Hls = FakeHls;
  const document = {
    hidden: false,
    head,
    addEventListener() {},
    removeEventListener() {},
    createElement(tag) {
      if (tag === "script") return { src: "", async: false, onload: null, onerror: null };
      return makeVideo();
    },
  };
  let Card;
  vm.runInNewContext(source, {
    window,
    document,
    URL,
    HTMLElement: class {
      constructor() { this.isConnected = true; }
      attachShadow() {
        this.shadowRoot = {
          querySelector(selector) {
            if (!elements.has(selector)) {
              elements.set(selector, selector === "video" ? makeVideo() : makeElement(selector));
            }
            return elements.get(selector);
          },
        };
        return this.shadowRoot;
      }
    },
    customElements: {
      define(_name, constructor) { Card = constructor; },
      get: () => Card,
      whenDefined: async () => {},
    },
    setTimeout: clock.setTimeout,
    clearTimeout: clock.clearTimeout,
    setInterval: clock.setInterval,
    clearInterval: clock.clearInterval,
  });
  const card = new Card();
  card.setConfig({ entity: "camera.test_direct", ...config });
  const hass = {
    states: { "camera.test_direct": cameraState() },
    async callWS(message) {
      calls.push(message);
      if (message.type === "camera/stream") return { url: "/api/hls/tok/master_playlist.m3u8" };
      return {};
    },
  };
  card.hass = hass;
  await flush();
  return { card, elements, calls, clock, hass, window, head };
}

test("card template builds a video plus a D-pad that starts hidden", () => {
  assert.match(source, /<video playsinline muted autoplay>/);
  for (const direction of ["up", "down", "left", "right"]) {
    assert.match(source, new RegExp(`class="ptz-${direction}"`));
  }
  assert.match(source, /class="ptz" hidden/);
});

test("streaming asks for the camera URL and starts hls.js with the low latency config", async () => {
  const { elements, calls } = await setUpCard();
  const hls = FakeHls.instances.at(-1);
  assert.ok(hls, "hls.js instance expected");
  assert.equal(calls[0].type, "camera/stream");
  assert.equal(calls[0].entity_id, "camera.test_direct");
  assert.equal(hls.source, "http://ha.local/api/hls/tok/master_playlist.m3u8");
  assert.equal(hls.media, elements.get("video"));
  assert.equal(hls.config.lowLatencyMode, true);
  assert.equal(hls.config.liveSyncDuration, 0.9);
  assert.equal(hls.config.maxLiveSyncPlaybackRate, 1.3);

  hls.emit("fragBuffered", {});
  assert.equal(elements.get(".overlay").hidden, true);
});

test("the card injects hls.js itself when the frontend has no global", async () => {
  const { head, elements } = await setUpCard({}, { withHls: false });
  assert.equal(head.children.length, 1);
  assert.equal(head.children[0].src, "/xiaomi-direct/hls.light.min.js");
  assert.equal(FakeHls.instances.length, 0);
  assert.match(elements.get(".overlay").textContent, /正在连接/);
});

test("the latency badge reports the distance to the live edge", async () => {
  const { elements, clock } = await setUpCard();
  clock.advance(1000);
  assert.equal(elements.get(".latency").hidden, false);
  assert.equal(elements.get(".latency").textContent, "1.6 s");
});

test("latency_indicator: false keeps the badge hidden", async () => {
  const { elements, clock } = await setUpCard({ latency_indicator: false });
  clock.advance(2000);
  assert.equal(elements.get(".latency").hidden, true);
});

test("the pad appears only for a camera that advertises PTZ", async () => {
  const { card, elements, hass } = await setUpCard();
  assert.equal(elements.get(".ptz").hidden, false);
  assert.equal(elements.get(".ptz").attrs["data-live"], "true");

  hass.states["camera.test_direct"] = cameraState({ ptz: false });
  card.hass = hass;
  assert.equal(elements.get(".ptz").hidden, true);
});

test("ptz: false hides the pad", async () => {
  const { elements } = await setUpCard({ ptz: false });
  assert.equal(elements.get(".ptz").hidden, true);
});

test("pressing a direction sends one authenticated step", async () => {
  const { elements, calls } = await setUpCard();
  const left = elements.get(".ptz-left");
  left.fire("pointerdown", {});
  await flush();
  const step = calls.find((call) => call.type === "xiaomi_direct_pilot/ptz/step");
  assert.equal(step.direction, "left");
  assert.equal(step.entity_id, "camera.test_direct");
  assert.equal(left.attrs["data-active"], "true");
  left.fire("pointerup", {});
  assert.equal(left.attrs["data-active"], "false");
});

test("holding a direction repeats the step and stops on release", async () => {
  const { elements, calls, clock } = await setUpCard();
  const up = elements.get(".ptz-up");
  up.fire("pointerdown", {});
  await flush();
  assert.equal(calls.filter((call) => call.direction === "up").length, 1);

  clock.advance(600);
  await flush();
  clock.advance(300);
  await flush();
  assert.equal(calls.filter((call) => call.direction === "up").length, 2);

  up.fire("pointerup", {});
  clock.advance(3000);
  await flush();
  assert.equal(calls.filter((call) => call.direction === "up").length, 2);
});

test("a rejected step shows the message and stops the hold", async () => {
  const { elements, clock, hass } = await setUpCard();
  hass.callWS = async (message) => {
    if (message.type === "xiaomi_direct_pilot/ptz/step") {
      throw new Error("Open the camera's live stream first");
    }
    return { url: "/api/hls/tok/master_playlist.m3u8" };
  };
  const right = elements.get(".ptz-right");
  right.fire("pointerdown", {});
  await flush();
  assert.match(elements.get(".ptz-error").textContent, /live stream/);
  assert.equal(right.attrs["data-active"], "false");

  clock.advance(4500);
  assert.equal(elements.get(".ptz-error").textContent, "");
});

test("a fatal hls error shows the reason and schedules a retry", async () => {
  const { card, elements, clock } = await setUpCard();
  const hls = FakeHls.instances.at(-1);
  hls.emit("hlsError", { fatal: true, type: "otherError", details: "bufferStalledError" });
  assert.equal(elements.get(".overlay").hidden, false);
  assert.match(elements.get(".overlay").textContent, /bufferStalledError/);
  assert.equal(hls.destroyed, true);
  assert.ok(clock.pending > 0, "a retry should be scheduled");
  assert.equal(card._hls, undefined);
});

test("a network error reloads instead of tearing the stream down", async () => {
  const { elements } = await setUpCard();
  const hls = FakeHls.instances.at(-1);
  hls.emit("hlsError", { fatal: true, type: "networkError", details: "manifestLoadError" });
  assert.equal(hls.reloaded, true);
  assert.equal(hls.destroyed, false);
  assert.match(elements.get(".overlay").textContent, /正在连接/);
});

test("disconnecting stops the stream and the timers", async () => {
  const { card, clock } = await setUpCard();
  const hls = FakeHls.instances.at(-1);
  card.disconnectedCallback();
  assert.equal(hls.destroyed, true);
  assert.equal(clock.pending, 0);
});
