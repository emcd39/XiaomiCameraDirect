const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const test = require("node:test");
const vm = require("node:vm");

const source = fs.readFileSync(path.join(__dirname, "../examples/home-assistant/xiaomi-live-camera.js"), "utf8");

async function setup() {
  let now = 0;
  let tick;
  let registered;
  const cards = [];
  const events = new Map();
  const element = (localName) => ({
    localName,
    children: [],
    append(...children) { this.children.push(...children); },
    replaceChildren(...children) { this.children = children; },
  });
  const document = {
    hidden: false,
    createElement: element,
    addEventListener(name, callback) { events.set(name, callback); },
    removeEventListener(name) { events.delete(name); },
  };
  vm.runInNewContext(source, {
    HTMLElement: class { attachShadow() { return element(); } },
    document,
    performance: { now: () => now },
    setInterval(callback) { tick = callback; return 1; },
    clearInterval() { tick = undefined; },
    customElements: { get() {}, define(_name, card) { registered = card; } },
    window: { async loadCardHelpers() {
      return { createCardElement(config) {
        const card = { ...element("hui-picture-elements-card"), config, shadowRoot: element() };
        cards.push(card);
        return card;
      } };
    } },
  });

  const config = {
    camera_image: "camera.c201", camera_view: "live",
    elements: [{ type: "icon", entity: "button.c201_ptz" }],
  };
  const card = new registered();
  card.setConfig(config);
  const camera = { state: "idle" };
  card.hass = { states: { [config.camera_image]: camera } };
  card.isConnected = true;
  card.connectedCallback();
  await card.helpersPromise;
  return {
    card, cards, camera, document,
    showVideo() {
      const video = { ...element("video"), currentTime: 0 };
      cards.at(-1).shadowRoot.append(video);
      return video;
    },
    at(seconds) { now = seconds * 1000; tick?.(); },
    visibility(hidden) { document.hidden = hidden; events.get("visibilitychange")?.(); },
  };
}

test("keeps advancing video and replaces only the stalled camera card", async () => {
  const env = await setup();
  const video = env.showVideo();
  for (let seconds = 5; seconds <= 40; seconds += 5) {
    video.currentTime = seconds;
    env.at(seconds);
  }
  assert.equal(env.cards.length, 1);
  env.at(64);
  assert.equal(env.cards.length, 1);
  env.at(65);
  assert.equal(env.cards.length, 2);
  assert.equal(env.cards[1].config.type, "picture-elements");
  assert.equal(env.cards[1].config.elements[0].entity, "button.c201_ptz");
  assert.equal(env.cards[1].hass.states["camera.c201"], env.camera);
});

test("retries a poster fallback with backoff and resets after playback resumes", async () => {
  const env = await setup();
  env.at(25);
  env.at(74);
  assert.equal(env.cards.length, 2);
  env.at(75);
  assert.equal(env.cards.length, 3);
  const video = env.showVideo();
  env.at(80);
  video.currentTime = 2;
  env.at(85);
  env.at(110);
  assert.equal(env.cards.length, 4);
});

test("suspends attempts while hidden, unavailable or unmounted", async () => {
  const env = await setup();
  env.visibility(true);
  env.at(90);
  env.visibility(false);
  env.at(91);
  env.camera.state = "unavailable";
  env.at(150);
  assert.equal(env.cards.length, 1);
  env.camera.state = "idle";
  env.at(174);
  assert.equal(env.cards.length, 1);
  env.at(175);
  assert.equal(env.cards.length, 2);
  env.card.disconnectedCallback();
  env.at(300);
  assert.equal(env.cards.length, 2);
});
