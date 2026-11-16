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
let nextPTZOutcome = 'simulated';
let holdNextStatusResponse = false;
let delayAllStatusResponses = false;
let statusInfoCode = 200;
let ptzEnabled = true;
let lastPTZResult = null;
let recordingError = '';
let recordingReconnecting = false;
let recordingReconnectAttempt = 0;
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

let inventory = {loaded:true,camera_count:1,c201_count:1,stream_urls_detected:0,cameras:[{id:'xiaomi_0123456789abcdef',name:'Mock C201',model:'xiaomi.camera.c201',video:true,video_ready:true,video_state:'streaming',session_active:true,ptz:true,selected:true}],c201_cameras:[{id:'xiaomi_0123456789abcdef',name:'Mock C201',model:'xiaomi.camera.c201',video:true,video_ready:true,video_state:'streaming',session_active:true,ptz:true,selected:true}],selected_camera_id:'xiaomi_0123456789abcdef',session_ready:true,session_active:true,session_state:'streaming',fetched_at:'2026-11-11T00:00:00Z'};
function statusResponse() {
  return {
    mock:true,
    gateway:{reachable:true,http_status:200,info_status:statusInfoCode,mode:'mock',inventory:inventoryLoaded?inventory:{loaded:false,camera_count:0,c201_count:0,stream_urls_detected:0,cameras:[],c201_cameras:[]},note:'SIMULATION: no camera'},
    video:{running:previewRunning,mock:true,frames:previewRunning?4:0,fps:previewRunning?3:0,last_frame_at:previewRunning?'2026-11-11T16:00:00Z':'',resolution:'848x480'},
    recording:{running:false,reconnecting:recordingReconnecting,reconnect_attempt:recordingReconnectAttempt,directory_configured:false,error:recordingError},
    ptz:{enabled:ptzEnabled,busy:false,last:lastPTZResult},
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
  else if (url.pathname === '/api/inventory/refresh' && method === 'POST') { previewRunning = false; inventoryLoaded = true; if(inventory.c201_count>1)inventory={...inventory,selected_camera_id:'',session_ready:false,session_active:false,session_state:'not_prepared',cameras:inventory.cameras.map(camera=>({...camera,selected:false,session_active:false})),c201_cameras:inventory.c201_cameras.map(camera=>({...camera,selected:false,session_active:false}))}; payload = inventory; }
  else if (url.pathname === '/api/inventory/select' && method === 'POST') { const cameraID=JSON.parse(init.body).camera_id;selectionRequests.push(cameraID);previewRunning=false;const selected=(inventory.cameras||inventory.c201_cameras).find(camera=>camera.id===cameraID);const active=Boolean(selected?.session_active||selected?.ptz);inventory={...inventory,selected_camera_id:cameraID,session_ready:Boolean(selected?.video_ready),session_active:active,session_state:selected?.video_state||'unsupported',cameras:(inventory.cameras||[]).map(camera=>({...camera,selected:camera.id===cameraID,session_active:camera.id===cameraID&&active})),c201_cameras:(inventory.c201_cameras||[]).map(camera=>({...camera,selected:camera.id===cameraID,session_active:camera.id===cameraID&&active}))};payload=inventory; }
  else if (url.pathname === '/api/ptz/step' && method === 'POST') {
    const requestBody = JSON.parse(init.body);
    const direction = requestBody.direction;
    ptzRequests.push(direction);
    ptzCameraRequests.push(requestBody.camera_id);
    const outcome=nextPTZOutcome;
    nextPTZOutcome='simulated';
    payload = {camera_id:requestBody.camera_id,outcome,direction,http_write_status:outcome==='sent'?'sent':'simulated_only',reply_0x113:'not_measured',motion:'not_measured',note:outcome==='sent'?'Gateway accepted one camera-scoped step':'Mock only'};
    if (holdPTZResponse) return new Promise(resolve => { resolvePTZResponse = () => { lastPTZResult=payload; resolve({ok:true,status:200,json:async()=>payload}); }; });
    lastPTZResult = payload;
  }
  else if (url.pathname === '/api/ptz/left' && method === 'POST') {
    ptzRequests.push('left');
    payload = {camera_id:inventory.selected_camera_id,outcome:'simulated',direction:'left',http_write_status:'simulated_only',reply_0x113:'not_measured',motion:'not_measured',note:'Mock only'};
    lastPTZResult = payload;
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

  inventory = {...inventory,session_ready:false,session_active:true,session_state:'idle',cameras:inventory.cameras.map(camera=>({...camera,video_ready:false,video_state:'idle',session_active:true})),c201_cameras:inventory.c201_cameras.map(camera=>({...camera,video_ready:false,video_state:'idle',session_active:true}))};
  await vm.runInContext('status()', context);
  assert.equal(getElement('startPreview').disabled, true, 'preview stays disabled until RTSP is ready');
  assert.equal(getElement('ptzLeft').disabled, false, 'PTZ is available for the active session before RTSP readiness');
  inventory = {...inventory,session_ready:true,session_active:true,session_state:'streaming',cameras:inventory.cameras.map(camera=>({...camera,video_ready:true,video_state:'streaming'})),c201_cameras:inventory.c201_cameras.map(camera=>({...camera,video_ready:true,video_state:'streaming'}))};
  await vm.runInContext('status()', context);

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
  const cameraAOption={id:cameraA,name:'C201 A',model:'xiaomi.camera.c201',video:true,video_ready:true,video_state:'streaming',session_active:true,ptz:true,selected:true};
  const cameraBOption={id:cameraB,name:'C201 B',model:'xiaomi.camera.c201',video:true,video_ready:true,video_state:'streaming',session_active:true,ptz:true,selected:false};
  inventory={loaded:true,camera_count:2,c201_count:2,stream_urls_detected:2,cameras:[cameraAOption,cameraBOption],c201_cameras:[cameraAOption,cameraBOption],selected_camera_id:cameraA,session_ready:true,session_active:true,session_state:'streaming'};
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

  const cameraC='xiaomi_3333333333333333';
  const cameraCOption={id:cameraC,name:'C400 test',model:'chuangmi.camera.039a04',video:true,video_ready:true,video_state:'streaming',ptz:false,selected:false};
  inventory={...inventory,camera_count:3,c201_count:2,cameras:[cameraAOption,cameraBOption,cameraCOption],c201_cameras:[cameraAOption,cameraBOption],selected_camera_id:cameraB};
  vm.runInContext(`renderInventory(${JSON.stringify(inventory)})`, context);
  assert.equal(cameraSelect.children.length, 3, 'the selector includes non-C201 cameras in the managed inventory');
  cameraSelect.value=cameraC;
  await vm.runInContext('selectCamera()',context);
  assert.equal(vm.runInContext('selectedCameraID',context),cameraC,'the non-C201 selection is adopted from the server');
  assert.equal(getElement('ptzLeft').disabled,true,'PTZ remains disabled for a camera without PTZ capability');
  const beforeUnsupportedPTZ=ptzRequests.length;
  await getElement('ptzLeft').onclick();
  assert.equal(ptzRequests.length,beforeUnsupportedPTZ,'a camera without PTZ sends no step request');

  const futurePTZ='lab_0000000000000004';
  const futurePTZOption={id:futurePTZ,name:'Unknown PTZ test',model:'vendor.camera.future-ptz',video:true,video_ready:true,video_state:'streaming',session_active:true,ptz:true,selected:false};
  inventory={...inventory,camera_count:4,c201_count:2,cameras:[cameraAOption,cameraBOption,cameraCOption,futurePTZOption],c201_cameras:[cameraAOption,cameraBOption],selected_camera_id:cameraB};
  vm.runInContext(`renderInventory(${JSON.stringify(inventory)})`,context);
  assert.equal(cameraSelect.children.length,4,'the selector includes an unknown model with explicitly advertised PTZ');
  cameraSelect.value=futurePTZ;
  await vm.runInContext('selectCamera()',context);
  assert.equal(vm.runInContext('selectedCameraID',context),futurePTZ,'unknown PTZ model is selected by its exact inventory ID');
  assert.equal(getElement('ptzLeft').disabled,false,'gateway-advertised PTZ and active session enable unknown-model controls');
  const beforeUnknownPTZ=ptzRequests.length;
  await getElement('ptzLeft').onclick();
  assert.deepEqual(ptzRequests.slice(beforeUnknownPTZ),['left'],'unknown-model left click sends exactly one step');
  assert.deepEqual(ptzCameraRequests.slice(beforeUnknownPTZ),[futurePTZ],'unknown-model step uses that camera ID');

  const singleCamera={loaded:true,camera_count:1,c201_count:0,cameras:[{...cameraCOption,selected:false}],c201_cameras:[]};
  vm.runInContext(`renderInventory(${JSON.stringify(singleCamera)})`,context);
  assert.equal(getElement('cameraSelectWrap').hidden,false,'a single unselected camera can be chosen');
  assert.equal(cameraSelect.children.length,2,'the selector offers a placeholder and the camera');
  inventory={...singleCamera,stream_urls_detected:1,cameras:[{...cameraCOption,selected:true}],selected_camera_id:cameraC,session_ready:true,session_state:'streaming'};
  inventoryLoaded=true;
  await vm.runInContext('status()',context);
  assert.equal(getElement('cameraSelectWrap').hidden,true,'single-camera chooser stays compact');
  assert.equal(getElement('startPreview').disabled,false,'a single non-C201 camera with video is previewable');
  await getElement('startPreview').onclick();
  assert.equal(previewRunning,true,'preview starts for the selected single non-C201 camera');
  ptzEnabled=false;
  await vm.runInContext('status()',context);
  assert.equal(getElement('ptzLeft').disabled,true,'PTZ controls honor disabled live configuration');
  const beforeDisabledPTZ=ptzRequests.length;
  await getElement('ptzLeft').onclick();
  assert.equal(ptzRequests.length,beforeDisabledPTZ,'disabled PTZ configuration sends no request');
  ptzEnabled=true;

  inventory={loaded:true,camera_count:2,c201_count:1,stream_urls_detected:2,cameras:[cameraAOption,cameraCOption],c201_cameras:[cameraAOption],selected_camera_id:cameraA,session_ready:true,session_state:'streaming'};
  cameraSelect.value=cameraA;
  await vm.runInContext('selectCamera()',context);
  lastPTZResult={camera_id:cameraA,outcome:'sent',direction:'left',http_write_status:'sent',reply_0x113:'not_measured',motion:'not_measured',note:'Gateway accepted one C201 step'};
  await vm.runInContext('status()',context);
  assert.equal(getElement('writeStatus').textContent,'sent','the C201 panel displays its own sent PTZ result');

  nextPTZOutcome='sent';
  holdPTZResponse=true;
  const cameraAPTzStart=ptzRequests.length;
  const delayedCameraAPTz=getElement('ptzLeft').onclick();
  await new Promise(resolve=>setImmediate(resolve));
  assert.deepEqual(ptzRequests.slice(cameraAPTzStart),['left'],'one delayed left request starts for C201');
  assert.deepEqual(ptzCameraRequests.slice(cameraAPTzStart),[cameraA],'the delayed request is correlated to C201');
  holdNextStatusResponse=true;
  const delayedCameraAStatus=vm.runInContext('status()',context);
  await new Promise(resolve=>setImmediate(resolve));
  assert.equal(delayedStatusResponses.at(-1).snapshot.ptz.last.camera_id,cameraA,'the held status contains the C201 result');

  cameraSelect.value=cameraC;
  await vm.runInContext('selectCamera()',context);
  assert.equal(vm.runInContext('selectedCameraID',context),cameraC,'the test switches from C201 to C400 while the C201 response is pending');
  assert.equal(getElement('ptzLeft').disabled,true,'PTZ stays disabled on C400');
  assert.equal(getElement('writeStatus').textContent,'—','switching to C400 clears the previous C201 write result');
  assert.equal(getElement('ptzDetail').textContent,'','switching to C400 clears the previous C201 detail');

  resolveNextDelayedStatusResponse();
  await delayedCameraAStatus;
  for(let i=0;i<10&&(vm.runInContext('statusInFlight',context)||currentStatusFetches>0);i++)await new Promise(resolve=>setImmediate(resolve));
  assert.equal(getElement('writeStatus').textContent,'—','a delayed C201 status cannot restore C201 result in the C400 panel');
  resolvePTZResponse();
  await delayedCameraAPTz;
  holdPTZResponse=false;
  assert.equal(getElement('writeStatus').textContent,'—','a delayed C201 PTZ response cannot write into the C400 panel');
  assert.equal(getElement('ptzDetail').textContent,'','a late response cannot leave C201 detail on C400');
  await vm.runInContext('status()',context);
  assert.equal(getElement('writeStatus').textContent,'—','the next status poll still hides the C201 result on C400');
  assert.equal(getElement('ptzLeft').disabled,true,'C400 remains PTZ-disabled after the stale result');

  recordingError = 'recording stopped unexpectedly; incomplete files are hidden';
  await vm.runInContext('status()', context);
  const storageCard = getElement('statusCards').children.find(card=>card.children[0].textContent==='Úložiště');
  assert.match(String(storageCard.children[1].textContent), /Chyba: recording stopped unexpectedly/,
    'a failed recording is visible in the status cards');
  recordingReconnecting = true;
  recordingReconnectAttempt = 2;
  await vm.runInContext('status()', context);
  const reconnectCard = getElement('statusCards').children.find(card=>card.children[0].textContent==='Úložiště');
  assert.equal(String(reconnectCard.children[1].textContent), 'Obnovuji záznam (pokus 2)',
    'the UI shows the numbered reconnect attempt');

  process.stdout.write(JSON.stringify({result:'PASS',environment:'Node test DOM; actual index.html, app.css and app.js; mock fetch/MJPEG',started,stopped,ptzRequests,ptzCameraRequests,selectionRequests,slowStatusLatencyMs:delayedStatusLatencyMs,slowStatusPolls:2,slowStatusResponses:2,maxConcurrentStatusFetches,selectedAfterReselection:vm.runInContext('selectedCameraID',context),previewHiddenOnSelection:image.hidden,previewHiddenOnRefresh:image.hidden,confirmCalls}) + '\n');
}

run().catch(error => {
  process.stderr.write(`${error.stack || error}\n`);
  process.exitCode = 1;
});
