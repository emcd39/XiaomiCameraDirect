const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const test = require("node:test");
const vm = require("node:vm");

const source = fs.readFileSync(path.join(__dirname,
  "../custom_components/xiaomi_direct_pilot/www/xiaomi-direct-camera.js"), "utf8");

async function setUpCard(media) {
  const controls = new Map();
  const video = { volume: 0.5, muted: true, addEventListener() {} };
  const icon = {};
  const player = {
    video,
    hasAudio: false,
    shadowRoot: { querySelector: () => icon },
    setConfig(config) { this.config = config; },
  };
  let Card;
  vm.runInNewContext(source, {
    window: { customCards: [] },
    HTMLElement: class {
      attachShadow() {
        return { querySelector(selector) {
          const control = { append() {}, addEventListener() {} };
          controls.set(selector, control);
          return control;
        } };
      }
    },
    customElements: {
      define(_name, constructor) { Card = constructor; },
      whenDefined: async () => {},
      get: () => Card,
    },
    document: { createElement: () => player },
  });
  const card = new Card();
  card.setConfig({ entity: "camera.test_direct", media, muted: true, style: "video { object-fit: contain; }" });
  await card._cameraReady;
  return { player, icon, slider: controls.get("input") };
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
