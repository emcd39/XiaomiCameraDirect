'use strict';

const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');

const staticDir = path.resolve(__dirname, '..', 'internal', 'console', 'static');
const statusPollIntervalMs = 3500;
const delayedStatusLatencyMs = 5000;
const html = fs.readFileSync(path.join(staticDir, 'index.html'), 'utf8');
const css = fs.readFileSync(path.join(staticDir, 'app.css'), 'utf8');
const appJS = fs.readFileSync(path.join(staticDir, 'app.js'), 'utf8');

const tagFor = (id) => {
  const tag = html.match(new RegExp(`<[^>]+\\bid=["']${id}["'][^>]*>`, 'i'));
  assert.ok(tag, `HTML element #${id} exists`);
  return tag[0];
};
const hasHiddenAttribute = (tag) => /\shidden(?:\s|=|\/?>)/i.test(tag);

assert.match(html, /href="\/app\.css"/, 'HTML links the embedded stylesheet');
assert.match(html, /src="\/app\.js"/, 'HTML links the embedded application script');
assert.match(css, /\[hidden\]\s*\{\s*display\s*:\s*none\s*!important\s*;?\s*\}/i,
  'CSS keeps the hidden attribute stronger than .view-placeholder display:flex');

const imageRequests = [];
const apiRequests = [];
const ptzRequests = [];
const ptzCameraRequests = [];
const selectionRequests = [];
let confirmCalls = 0;
let previewRunning = false;
let inventoryLoaded = false;
let holdPTZResponse = false;
let resolvePTZResponse;
let holdNextStatusResponse = false;
let delayAllStatusResponses = false;
let statusInfoCode = 200;
let currentStatusFetches = 0;
let maxConcurrentStatusFetches = 0;
const delayedStatusResponses = [];
const intervalCallbacks = [];

function resolveNextDelayedStatusResponse() {
  const pending = delayedStatusResponses.shift();
  assert.ok(pending, 'a delayed status response is waiting');
  pending.release();
}

class FakeClassList {
  constructor() { this.values = new Set(); }
  toggle(name, force) {
    const enabled = force === undefined ? !this.values.has(name) : Boolean(force);
    if (enabled) this.values.add(name);
    else this.values.delete(name);
    return enabled;
  }
}

class FakeElement {
  constructor(id = '', hidden = false) {
    this.id = id;
    this.hidden = hidden;
    this.disabled = false;
    this.checked = false;
    this.value = '';
    this.textContent = '';
    this.className = '';
    this.classList = new FakeClassList();
    this.children = [];
    this.onclick = null;
  }
  replaceChildren(...children) { this.children = [...children]; }
  append(...children) { this.children.push(...children); }
  appendChild(child) { this.children.push(child); return child; }
}

class FakeImage extends FakeElement {
  constructor(id, hidden) {
    super(id, hidden);
    this.complete = false;
    this.naturalWidth = 0;
    this.naturalHeight = 0;
    this._src = '';
  }
  get src() { return this._src; }
  set src(value) {
    if (typeof value === 'string' && value.startsWith('/api/video/mjpeg')) {
      imageRequests.push(value);
      this._src = 'mock-jpeg://848x480';
      this.complete = true;
      this.naturalWidth = 848;
      this.naturalHeight = 480;
      return;
    }
    this._src = value;
    this.complete = false;
    this.naturalWidth = 0;
    this.naturalHeight = 0;
  }
}

const elements = new Map();
function getElement(id) {
  if (!elements.has(id)) {
    const hidden = id === 'videoImage' ? hasHiddenAttribute(tagFor(id)) : hasHiddenAttribute(tagFor(id));
    const element = id === 'videoImage' ? new FakeImage(id, hidden) : new FakeElement(id, hidden);
    elements.set(id, element);
  }
  return elements.get(id);
}

let inventory = {loaded:true,camera_count:1,c201_count:1,stream_urls_detected:0,c201_cameras:[{id:'xiaomi_0123456789abcdef',name:'Mock C201'}],selected_camera_id:'xiaomi_0123456789abcdef',session_ready:true,session_state:'streaming',fetched_at:'2026-11-11T00:00:00Z'};
function statusResponse() {
  return {
    mock:true,
    gateway:{reachable:true,http_status:200,info_status:statusInfoCode,mode:'mock',inventory:inventoryLoaded?inventory:{loaded:false,camera_count:0,c201_count:0,stream_urls_detected:0,c201_cameras:[]},note:'SIMULATION: no camera'},
    video:{running:previewRunning,mock:true,frames:previewRunning?4:0,fps:previewRunning?3:0,last_frame_at:previewRunning?'2026-11-11T16:00:00Z':'',resolution:'848x480'},
    recording:{running:false,directory_configured:false},
    ptz:{enabled:true,busy:false,last:null},
    camera_address_verified:false,
  };
}

async function mockFetch(input, init = {}) {
  const url = new URL(typeof input === 'string' ? input : input.url, 'http://127.0.0.1:18880');
  const method = String(init.method || 'GET').toUpperCase();
  apiRequests.push(`${method} ${url.pathname}`);
  let payload;
  if (url.pathname === '/api/status' && method === 'GET') {
    payload = statusResponse();
    currentStatusFetches++;
    maxConcurrentStatusFetches = Math.max(maxConcurrentStatusFetches,currentStatusFetches);
    const delayResponse = holdNextStatusResponse || delayAllStatusResponses;
    holdNextStatusResponse = false;
    if (delayResponse) {
      const snapshot = JSON.parse(JSON.stringify(payload));
      return new Promise(resolve => delayedStatusResponses.push({snapshot,release:()=>{
        currentStatusFetches--;
        resolve({ok:true,status:200,json:async()=>snapshot});
      }}));
    }
    currentStatusFetches--;
  }
  else if (url.pathname === '/api/events' && method === 'GET') payload = [];
  else if (url.pathname === '/api/record/files' && method === 'GET') payload = [];
  else if (url.pathname === '/api/capabilities' && method === 'GET') payload = [];
  else if (url.pathname === '/api/inventory/refresh' && method === 'POST') { previewRunning = false; inventoryLoaded = true; if(inventory.c201_count>1)inventory={...inventory,selected_camera_id:'',session_ready:false,session_state:'not_prepared'}; payload = inventory; }
  else if (url.pathname === '/api/inventory/select' && method === 'POST') { const cameraID=JSON.parse(init.body).camera_id;selectionRequests.push(cameraID);previewRunning=false;inventory={...inventory,selected_camera_id:cameraID,session_ready:true,session_state:'streaming'};payload=inventory; }
  else if (url.pathname === '/api/ptz/step' && method === 'POST') {
    const requestBody = JSON.parse(init.body);
    const direction = requestBody.direction;
    ptzRequests.push(direction);
    ptzCameraRequests.push(requestBody.camera_id);
    payload = {outcome:'simulated',direction,http_write_status:'simulated_only',reply_0x113:'not_measured',motion:'not_measured',note:'Mock only'};
    if (holdPTZResponse) return new Promise(resolve => { resolvePTZResponse = () => resolve({ok:true,status:200,json:async()=>payload}); });
  }
  else if (url.pathname === '/api/ptz/left' && method === 'POST') {
    ptzRequests.push('left');
    payload = {outcome:'simulated',direction:'left',http_write_status:'simulated_only',reply_0x113:'not_measured',motion:'not_measured',note:'Mock only'};
  }
  else if (url.pathname === '/api/video/start' && method === 'POST') { previewRunning = true; payload = {result:'started'}; }
  else if (url.pathname === '/api/video/stop' && method === 'POST') { previewRunning = false; payload = {result:'stopped'}; }
  else return {ok:false,status:404,json:async()=>({error:`unhandled mock request: ${method} ${url.pathname}`})};
  return {ok:true,status:200,json:async()=>payload};
}

const document = {
  getElementById: getElement,
  createElement: (tag) => new FakeElement(tag),
  createTextNode: (text) => ({textContent:String(text)}),
};
const context = vm.createContext({
  document,
  fetch:mockFetch,
  URL,
  Date,
  Promise,
  Number,
  Array,
  setInterval:(callback,delay)=>{intervalCallbacks.push({callback,delay});return intervalCallbacks.length},
  clearInterval:()=>{},
  setTimeout:()=>0,
  clearTimeout:()=>{},
  confirm:()=>{confirmCalls++;return true},
  console,
});

async function run() {
  vm.runInContext(appJS, context, {filename:path.join(staticDir, 'app.js')});
  await new Promise(resolve => setImmediate(resolve));
  assert.doesNotMatch(html, /id=["'](?:phase0|confirmation)["']/i, 'confirmation controls are absent from the page');
  assert.equal(apiRequests.filter(request => request === 'POST /api/inventory/refresh').length, 1, 'startup prepares inventory once');

  const placeholder = getElement('videoPlaceholder');
  const image = getElement('videoImage');
  assert.equal(placeholder.hidden, false, 'placeholder starts visible');
  assert.equal(image.hidden, true, 'image starts hidden');

  await getElement('startPreview').onclick();
  const started = {
    placeholderHidden:placeholder.hidden,
    imageHidden:image.hidden,
    imageLoaded:image.complete && image.naturalWidth > 0,
    frameCount:getElement('frameCount').textContent,
    imageRequest:imageRequests.at(-1) || null,
  };
  assert.equal(started.placeholderHidden, true, 'placeholder is hidden while preview runs');
  assert.equal(started.imageHidden, false, 'video image is shown while preview runs');
  assert.equal(started.imageLoaded, true, 'mock MJPEG response supplies a test image');
  assert.ok(Number(started.frameCount) > 0, 'UI reports frames while preview runs');

  await getElement('stopPreview').onclick();
  const stopped = {
    placeholderHidden:placeholder.hidden,
    imageHidden:image.hidden,
    imageSource:image.src,
    videoStatus:getElement('videoStatus').textContent,
  };
  assert.equal(stopped.placeholderHidden, false, 'placeholder returns after stopping preview');
  assert.equal(stopped.imageHidden, true, 'video image is hidden after stopping preview');
  assert.equal(stopped.imageSource, '', 'stopping clears the MJPEG image source');
  assert.equal(stopped.videoStatus, 'Vypnuto', 'UI reports preview stopped');
  assert.ok(apiRequests.includes('POST /api/video/start'));
  assert.ok(apiRequests.includes('POST /api/video/stop'));

  holdPTZResponse = true;
  const firstClick = getElement('ptzLeft').onclick();
  await new Promise(resolve => setImmediate(resolve));
  assert.deepEqual(ptzRequests, ['left'], 'one left click sends one step request');
  assert.equal(getElement('ptzLeft').disabled, true, 'PTZ button is disabled while the request is pending');
  await vm.runInContext('status()', context);
  await getElement('ptzLeft').onclick();
  assert.deepEqual(ptzRequests, ['left'], 'status polling cannot duplicate an in-flight click');
  resolvePTZResponse();
  await firstClick;
  assert.equal(getElement('ptzLeft').disabled, false, 'PTZ button is restored after completion');
  holdPTZResponse = false;

  for (const direction of ['right','up','down']) await getElement(`ptz${direction[0].toUpperCase()}${direction.slice(1)}`).onclick();
  assert.deepEqual(ptzRequests, ['left','right','up','down'], 'each arrow sends one matching direction');
  assert.deepEqual(ptzCameraRequests, Array(4).fill(inventory.selected_camera_id), 'every arrow sends the C201 ID displayed in the card');
  assert.equal(confirmCalls, 0, 'PTZ interactions never open a confirmation dialog');
  assert.equal(apiRequests.filter(request => request === 'POST /api/inventory/refresh').length, 1, 'status polling does not refresh inventory');

  const cameraA='xiaomi_1111111111111111';
  const cameraB='xiaomi_2222222222222222';
  inventory={loaded:true,camera_count:2,c201_count:2,stream_urls_detected:2,c201_cameras:[{id:cameraA,name:'C201 A'},{id:cameraB,name:'C201 B'}],selected_camera_id:cameraA,session_ready:true,session_state:'streaming'};
  vm.runInContext(`renderInventory(${JSON.stringify(inventory)})`, context);
  const cameraSelect = getElement('cameraSelect');
  assert.equal(cameraSelect.children.length, 2, 'both C201 cameras are available in the selector');
  await getElement('startPreview').onclick();
  assert.equal(image.hidden, false, 'preview is visible before changing cameras');
  cameraSelect.value = cameraB;
  await vm.runInContext('selectCamera()', context);
  assert.deepEqual(selectionRequests, [cameraB], 'the selected C201 ID is sent explicitly');
  assert.equal(previewRunning, false, 'camera selection change stops the old preview');
  assert.equal(image.hidden, true, 'old camera frame is hidden after selection change');
  assert.equal(image.src, '', 'old camera MJPEG source is cleared after selection change');
  assert.equal(placeholder.hidden, false, 'placeholder returns after selection change');

  await getElement('startPreview').onclick();
  assert.equal(image.hidden, false, 'preview can be restarted after camera selection');
  holdNextStatusResponse = true;
  const delayedCameraBStatus = vm.runInContext('status()', context);
  await new Promise(resolve => setImmediate(resolve));
  assert.equal(delayedStatusResponses[0].snapshot.gateway.inventory.selected_camera_id, cameraB, 'held status captures the old camera B selection');
  assert.equal(delayedStatusResponses[0].snapshot.video.running, true, 'held status captures the old running preview');
  await vm.runInContext('refreshInventory()', context);
  assert.equal(previewRunning, false, 'inventory refresh stops the active preview');
  assert.equal(image.hidden, true, 'old camera frame is hidden after inventory refresh');
  assert.equal(image.src, '', 'old camera MJPEG source is cleared after inventory refresh');
  assert.equal(vm.runInContext('selectedCameraID', context), '', 'UI clears the previous local ID when the server reports no selected camera');
  assert.equal(cameraSelect.children[0].value, '', 'multiple cameras show the choose-camera placeholder after refresh');
  assert.equal(getElement('ptzLeft').disabled, true, 'PTZ arrows stay disabled until a camera is selected');
  resolveNextDelayedStatusResponse();
  await delayedCameraBStatus;
  assert.equal(vm.runInContext('selectedCameraID', context), '', 'late camera B status cannot restore the old local selection');
  assert.equal(getElement('ptzLeft').disabled, true, 'late camera B status cannot re-enable PTZ arrows');
  assert.equal(image.hidden, true, 'late camera B status cannot restore the old preview');
  const beforeUnselectedClick = ptzRequests.length;
  await getElement('ptzLeft').onclick();
  assert.equal(ptzRequests.length, beforeUnselectedClick, 'an unselected inventory cannot send a PTZ request');

  cameraSelect.value = cameraB;
  await vm.runInContext('selectCamera()', context);
  assert.deepEqual(selectionRequests, [cameraB,cameraB], 'camera B can be explicitly selected again after refresh');
  assert.equal(vm.runInContext('selectedCameraID', context), cameraB, 'UI adopts camera B after the server confirms selection');
  const beforeReselectedClick = ptzRequests.length;
  await getElement('ptzLeft').onclick();
  assert.deepEqual(ptzRequests.slice(beforeReselectedClick), ['left'], 'one click after reselect sends one PTZ step');
  assert.deepEqual(ptzCameraRequests.slice(beforeReselectedClick), [cameraB], 'reselected PTZ step targets camera B');

  const statusPoll = intervalCallbacks.find(entry=>entry.delay===statusPollIntervalMs)?.callback;
  assert.equal(typeof statusPoll, 'function', 'the 3.5-second status poll is installed');
  assert.ok(delayedStatusLatencyMs > statusPollIntervalMs, 'the delayed status response exceeds the polling interval');
  const beforeSlowPolling = apiRequests.filter(request=>request==='GET /api/status').length;
  statusInfoCode = 200;
  delayAllStatusResponses = true;
  const slowStatusCycle = vm.runInContext('status()', context);
  let slowStatusCycleCompleted = false;
  slowStatusCycle.then(()=>{slowStatusCycleCompleted=true});
  await new Promise(resolve => setImmediate(resolve));
  assert.equal(delayedStatusResponses.length, 1, 'the first slow status response is held');
  statusInfoCode = 201;
  statusPoll();
  assert.equal(apiRequests.filter(request=>request==='GET /api/status').length, beforeSlowPolling + 1, 'poll ticks do not start overlapping status requests');
  const beforeSlowPTZ = ptzRequests.length;
  await getElement('ptzRight').onclick();
  assert.deepEqual(ptzRequests.slice(beforeSlowPTZ), ['right'], 'a PTZ click completes while polling is slow');
  assert.deepEqual(ptzCameraRequests.slice(beforeSlowPTZ), [cameraB], 'slow polling preserves the selected camera ID');
  assert.equal(getElement('ptzLeft').disabled, true, 'arrows wait for the fresh status after PTZ completion');
  assert.equal(maxConcurrentStatusFetches, 1, 'slow polling keeps at most one status HTTP request in flight');
  resolveNextDelayedStatusResponse();
  await new Promise(resolve => setImmediate(resolve));
  assert.equal(delayedStatusResponses.length, 1, 'queued polling starts a second slow status response');
  assert.equal(slowStatusCycleCompleted, true, 'the original status call completes while the next response is still pending');
  assert.equal(apiRequests.filter(request=>request==='GET /api/status').length, beforeSlowPolling + 2, 'repeated poll ticks still create only one queued request');
  assert.equal(getElement('ptzLeft').disabled, false, 'the completed PTZ step re-enables arrows when its slow response arrives');
  statusInfoCode = 202;
  statusPoll();
  assert.equal(apiRequests.filter(request=>request==='GET /api/status').length, beforeSlowPolling + 2, 'poll ticks do not overlap the second slow request');
  delayAllStatusResponses = false;
  resolveNextDelayedStatusResponse();
  await slowStatusCycle;
  for (let i=0; i<10 && vm.runInContext('statusInFlight',context); i++) await new Promise(resolve=>setImmediate(resolve));
  assert.equal(vm.runInContext('statusInFlight',context), false, 'the detached follow-up status cycle eventually completes');
  assert.equal(apiRequests.filter(request=>request==='GET /api/status').length, beforeSlowPolling + 3, 'the coalesced follow-up refreshes status after repeated slow responses');
  assert.equal(currentStatusFetches, 0, 'all status responses have completed');
  assert.equal(maxConcurrentStatusFetches, 1, 'slow five-second responses never overlap');
  const infoCard = getElement('statusCards').children.find(card=>card.children[0].textContent==='API /v2/info');
  assert.equal(String(infoCard.children[1].textContent), '202', 'the queued status updates the UI with the latest response');
  assert.equal(getElement('ptzLeft').disabled, false, 'PTZ arrows are re-enabled after the slow status completes');

  process.stdout.write(JSON.stringify({result:'PASS',environment:'Node test DOM; actual index.html, app.css and app.js; mock fetch/MJPEG',started,stopped,ptzRequests,ptzCameraRequests,selectionRequests,slowStatusLatencyMs:delayedStatusLatencyMs,slowStatusPolls:2,slowStatusResponses:2,maxConcurrentStatusFetches,selectedAfterReselection:vm.runInContext('selectedCameraID',context),previewHiddenOnSelection:image.hidden,previewHiddenOnRefresh:image.hidden,confirmCalls}) + '\n');
}

run().catch(error => {
  process.stderr.write(`${error.stack || error}\n`);
  process.exitCode = 1;
});
