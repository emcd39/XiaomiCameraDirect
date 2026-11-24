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
    textContent: "", hidden: false, disabled: false, value: 100,
    volume: 1, muted: true,
    get valueAsNumber() { return Number(this.value); },
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
      talkback_enabled: false,
      ...(ptz ? { ptz_directions: ["left", "right", "up", "down"], ptz_enabled: live } : {}),
    },
  };
}

async function setUpCard(media, config = {}) {
  const video = {
    volume: 0.5, muted: true,
    addEventListener() {}, removeEventListener() {},
  };
  const icon = {};
  const player = {
    video,
    hasAudio: false,
    shadowRoot: { querySelector: () => icon },
    setConfig(value) { this.config = value; },
  };
  const clock = createClock();
  const elements = new Map();
  const calls = [];
  let Card;
  vm.runInNewContext(source, {
    window: { customCards: [] },
    HTMLElement: class {
      attachShadow() {
        this.shadowRoot = {
          querySelector(selector) {
            if (!elements.has(selector)) elements.set(selector, makeElement(selector));
            return elements.get(selector);
          },
        };
        return this.shadowRoot;
      }
    },
    customElements: {
      define(_name, constructor) { Card = constructor; },
      whenDefined: async () => {},
      get: () => Card,
    },
    document: {
      createElement: () => player,
      addEventListener() {},
      removeEventListener() {},
    },
    setTimeout: clock.setTimeout,
    clearTimeout: clock.clearTimeout,
    setInterval: clock.setInterval,
    clearInterval: clock.clearInterval,
  });
  const card = new Card();
  // The template ships the pad hidden; the fake DOM does not parse innerHTML.
  elements.get(".ptz").hidden = true;
  card.setConfig({
    entity: "camera.test_direct", media, muted: true,
    style: "video { object-fit: contain; }", ...config,
  });
  await card._cameraReady;
  const hass = {
    states: { "camera.test_direct": cameraState() },
    async callWS(message) { calls.push(message); return {}; },
  };
  return { card, player, icon, elements, calls, clock, hass, slider: elements.get("input") };
}

test("audio card keeps the native mute control before MSE detects an audio track", async () => {
  const { player, icon, slider } = await setUpCard("video,audio");
  assert.match(player.config.style, /video \{ object-fit: contain; \}/);
  assert.match(player.config.style, /\.ui \.volume \{ display: block !important; \}/);
  assert.equal(icon.icon, "mdi:volume-mute");
  assert.equal(slider.disabled, false);
});

test("video-only card retains native volume visibility", async () => {
  const { player, slider } = await setUpCard("video");
  assert.doesNotMatch(player.config.style, /\.volume/);
  assert.equal(slider.disabled, true);
});

test("card template ships a directional pad that stays hidden without a PTZ profile", async () => {
  for (const direction of ["up", "down", "left", "right"]) {
    assert.match(source, new RegExp(`class="ptz-button ptz-${direction}"`));
  }
  const { card, elements, hass } = await setUpCard("video");
  const pad = elements.get(".ptz");
  assert.equal(pad.hidden, true);

  card.hass = hass;
  assert.equal(pad.hidden, false);
  assert.equal(pad.attrs["data-live"], "true");

  hass.states["camera.test_direct"] = cameraState({ live: false });
  card.hass = hass;
  assert.equal(pad.attrs["data-live"], "false");

  hass.states["camera.test_direct"] = cameraState({ ptz: false });
  card.hass = hass;
  assert.equal(pad.hidden, true);
});

test("ptz: false in the card config suppresses the pad", async () => {
  const { card, elements, hass } = await setUpCard("video", { ptz: false });
  card.hass = hass;
  assert.equal(elements.get(".ptz").hidden, true);
  assert.equal(elements.get(".ptz-up").listeners.pointerdown.length, 1);
});

test("pressing a direction sends one authenticated step", async () => {
  const { card, elements, calls, hass } = await setUpCard("video");
  card.hass = hass;
  const left = elements.get(".ptz-left");
  left.fire("pointerdown", {});
  await flush();
  assert.equal(calls.length, 1);
  assert.equal(calls[0].type, "xiaomi_direct_pilot/ptz/step");
  assert.equal(calls[0].entity_id, "camera.test_direct");
  assert.equal(calls[0].direction, "left");
  assert.equal(left.attrs["data-active"], "true");
  left.fire("pointerup", {});
  assert.equal(left.attrs["data-active"], "false");
});

test("holding a direction repeats the step and stops on release", async () => {
  const { card, elements, calls, hass, clock } = await setUpCard("video");
  card.hass = hass;
  const up = elements.get(".ptz-up");
  up.fire("pointerdown", {});
  await flush();
  assert.equal(calls.length, 1);

  clock.advance(600);
  await flush();
  clock.advance(300);
  await flush();
  assert.equal(calls.filter((call) => call.direction === "up").length, 2);

  up.fire("pointerup", {});
  clock.advance(3000);
  await flush();
  assert.equal(calls.filter((call) => call.direction === "up").length, 2);
  assert.equal(clock.pending, 0);
});

test("a rejected step shows the server message, stops the hold, then clears", async () => {
  const { card, elements, hass, clock } = await setUpCard("video");
  hass.callWS = async () => { throw new Error("Open the camera's live stream first"); };
  card.hass = hass;
  const right = elements.get(".ptz-right");
  right.fire("pointerdown", {});
  await flush();
  assert.match(elements.get(".ptz-error").textContent, /live stream/);
  assert.equal(right.attrs["data-active"], "false");

  clock.advance(4500);
  await flush();
  assert.equal(elements.get(".ptz-error").textContent, "");
});

test("disconnecting the card releases a held direction", async () => {
  const { card, elements, hass, clock } = await setUpCard("video");
  card.hass = hass;
  elements.get(".ptz-down").fire("pointerdown", {});
  await flush();
  card.disconnectedCallback();
  assert.equal(clock.pending, 0);
  assert.equal(elements.get(".ptz-down").attrs["data-active"], "false");
});
