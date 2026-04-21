package browser

// consoleInitScript hijacks console.log/info/warn/error/debug and stores
// entries in window.__drission_console_logs__ for later retrieval.
const consoleInitScript = `
(function() {
  if (window.__drission_console_initialized__) return;
  window.__drission_console_initialized__ = true;
  window.__drission_console_logs__ = [];
  const maxLogs = 500;
  const orig = {};
  ['log','info','warn','error','debug'].forEach(function(type) {
    orig[type] = console[type];
    console[type] = function() {
      var msg = Array.prototype.slice.call(arguments).map(function(a) {
        try { return typeof a === 'object' ? JSON.stringify(a) : String(a); }
        catch(e) { return String(a); }
      }).join(' ');
      if (window.__drission_console_logs__.length < maxLogs) {
        window.__drission_console_logs__.push({
          type: type,
          message: msg,
          timestamp: Date.now()
        });
      }
      orig[type].apply(console, arguments);
    };
  });
})();
`

// antiDetectScript provides 16 anti-detection measures to make the browser
// appear as a regular user-controlled Chrome instance.
const antiDetectScript = `
(function() {
  'use strict';

  // §1 — Remove webdriver flag
  try {
    Object.defineProperty(navigator, 'webdriver', { get: () => undefined });
    delete navigator.__proto__.webdriver;
  } catch(e) {}

  // §2 — Fake window.chrome
  try {
    if (!window.chrome) {
      window.chrome = {};
    }
    if (!window.chrome.runtime) {
      window.chrome.runtime = {
        connect: function() {},
        sendMessage: function() {},
        onMessage: { addListener: function() {} },
        id: undefined
      };
    }
    if (!window.chrome.app) {
      window.chrome.app = {
        isInstalled: false,
        InstallState: { DISABLED: 'disabled', INSTALLED: 'installed', NOT_INSTALLED: 'not_installed' },
        RunningState: { CANNOT_RUN: 'cannot_run', READY_TO_RUN: 'ready_to_run', RUNNING: 'running' },
        getDetails: function() { return null; },
        getIsInstalled: function() { return false; }
      };
    }
    if (!window.chrome.csi) {
      window.chrome.csi = function() { return { startE: Date.now(), onloadT: Date.now() + 100 }; };
    }
    if (!window.chrome.loadTimes) {
      window.chrome.loadTimes = function() {
        return {
          commitLoadTime: Date.now() / 1000,
          connectionInfo: 'h2',
          finishDocumentLoadTime: Date.now() / 1000 + 0.1,
          finishLoadTime: Date.now() / 1000 + 0.2,
          firstPaintAfterLoadTime: 0,
          firstPaintTime: Date.now() / 1000 + 0.05,
          navigationType: 'Other',
          npnNegotiatedProtocol: 'h2',
          requestTime: Date.now() / 1000 - 0.5,
          startLoadTime: Date.now() / 1000 - 0.5,
          wasAlternateProtocolAvailable: false,
          wasFetchedViaSpdy: true,
          wasNpnNegotiated: true
        };
      };
    }
  } catch(e) {}

  // §3 — Permissions API
  try {
    var origQuery = navigator.permissions.query;
    navigator.permissions.query = function(params) {
      if (params && params.name === 'notifications') {
        return Promise.resolve({ state: Notification.permission });
      }
      return origQuery.call(navigator.permissions, params);
    };
  } catch(e) {}

  // §4 — Plugins & MimeTypes
  try {
    var pluginData = [
      { name: 'Chrome PDF Plugin', filename: 'internal-pdf-viewer', description: 'Portable Document Format', mimeType: 'application/x-google-chrome-pdf' },
      { name: 'Chrome PDF Viewer', filename: 'mhjfbmdgcfjbbpaeojofohoefgiehjai', description: '', mimeType: 'application/pdf' },
      { name: 'Native Client', filename: 'internal-nacl-plugin', description: '', mimeType: 'application/x-nacl' }
    ];
    var fakePlugins = Object.create(PluginArray.prototype);
    pluginData.forEach(function(p, i) {
      var plugin = Object.create(Plugin.prototype);
      Object.defineProperties(plugin, {
        name: { value: p.name }, filename: { value: p.filename },
        description: { value: p.description }, length: { value: 1 }
      });
      Object.defineProperty(fakePlugins, i, { value: plugin });
    });
    Object.defineProperty(fakePlugins, 'length', { value: pluginData.length });
    Object.defineProperty(navigator, 'plugins', { get: function() { return fakePlugins; } });
  } catch(e) {}

  // §5 — Language & Platform & Hardware
  try {
    Object.defineProperty(navigator, 'languages', { get: function() { return ['zh-CN','zh','en-US','en']; } });
    Object.defineProperty(navigator, 'language', { get: function() { return 'zh-CN'; } });
    Object.defineProperty(navigator, 'platform', { get: function() { return 'Win32'; } });
    Object.defineProperty(navigator, 'vendor', { get: function() { return 'Google Inc.'; } });
    Object.defineProperty(navigator, 'hardwareConcurrency', { get: function() { return 4; } });
    try { Object.defineProperty(navigator, 'deviceMemory', { get: function() { return 8; } }); } catch(e2) {}
  } catch(e) {}

  // §6 — Canvas fingerprint noise (deterministic seed)
  try {
    var origToDataURL = HTMLCanvasElement.prototype.toDataURL;
    HTMLCanvasElement.prototype.toDataURL = function(type) {
      var ctx = this.getContext('2d');
      if (ctx) {
        var imgData = ctx.getImageData(0, 0, Math.min(this.width, 16), Math.min(this.height, 16));
        for (var i = 0; i < imgData.data.length; i += 4) {
          imgData.data[i] ^= ((i * 13 + 7) & 0xFF) > 253 ? 1 : 0;
        }
        ctx.putImageData(imgData, 0, 0);
      }
      return origToDataURL.apply(this, arguments);
    };
  } catch(e) {}

  // §7 — WebGL Renderer (WebGL1 + WebGL2)
  try {
    var patchWebGL = function(proto) {
      var origGetParam = proto.getParameter;
      proto.getParameter = function(p) {
        if (p === 37445) return 'Intel Inc.';
        if (p === 37446) return 'Intel Iris OpenGL Engine';
        return origGetParam.call(this, p);
      };
    };
    patchWebGL(WebGLRenderingContext.prototype);
    try { patchWebGL(WebGL2RenderingContext.prototype); } catch(e2) {}
  } catch(e) {}

  // §8 — hasFocus always true
  try {
    Document.prototype.hasFocus = function() { return true; };
  } catch(e) {}

  // §9 — Window dimensions
  try {
    if (window.outerWidth === 0) {
      Object.defineProperty(window, 'outerWidth', { get: function() { return window.innerWidth; } });
      Object.defineProperty(window, 'outerHeight', { get: function() { return window.innerHeight + 85; } });
    }
  } catch(e) {}

  // §10 — Screen properties
  try {
    Object.defineProperty(screen, 'availWidth', { get: function() { return screen.width; } });
    Object.defineProperty(screen, 'colorDepth', { get: function() { return 24; } });
    Object.defineProperty(screen, 'pixelDepth', { get: function() { return 24; } });
  } catch(e) {}

  // §11 — AudioContext fingerprint noise
  try {
    var origGetFrequencyData = AnalyserNode.prototype.getFloatFrequencyData;
    AnalyserNode.prototype.getFloatFrequencyData = function(array) {
      origGetFrequencyData.call(this, array);
      for (var i = 0; i < array.length; i++) {
        array[i] += (Math.random() - 0.5) * 0.001;
      }
    };
  } catch(e) {}

  // §12 — Battery API
  try {
    Object.defineProperty(navigator, 'getBattery', {
      value: function() {
        return Promise.resolve({
          charging: true, chargingTime: 0,
          dischargingTime: Infinity, level: 1.0,
          addEventListener: function() {}
        });
      }
    });
  } catch(e) {}

  // §13 — Connection API
  try {
    if (!navigator.connection) {
      Object.defineProperty(navigator, 'connection', {
        get: function() {
          return {
            effectiveType: '4g', downlink: 10, rtt: 50,
            saveData: false, type: 'wifi',
            addEventListener: function() {}
          };
        }
      });
    }
  } catch(e) {}

  // §14 — WebRTC leak protection
  try {
    var origSetRemoteDesc = RTCPeerConnection.prototype.setRemoteDescription;
    RTCPeerConnection.prototype.setRemoteDescription = function(desc) {
      if (desc && desc.sdp) {
        desc.sdp = desc.sdp.replace(/a=candidate:.*srflx.*/g, '')
                            .replace(/a=candidate:.*relay.*/g, '');
      }
      return origSetRemoteDesc.apply(this, arguments);
    };
  } catch(e) {}

  // §15 — MediaDevices
  try {
    var origEnumDevices = navigator.mediaDevices.enumerateDevices;
    navigator.mediaDevices.enumerateDevices = function() {
      return origEnumDevices.call(navigator.mediaDevices).then(function(devices) {
        return devices.map(function(d) {
          return { deviceId: d.deviceId, groupId: d.groupId, kind: d.kind, label: '' };
        });
      });
    };
  } catch(e) {}

  // §16 — Remove CDP/Selenium/automation variables
  try {
    var autoKeys = Object.getOwnPropertyNames(window).filter(function(k) {
      return /^cdc_|^__playwright|^__pw_|^__selenium|^_Selenium_IDE_Recorder|^__webdriver_evaluate|^__driver_evaluate|^__webdriver_unwrap|^__fxdriver_evaluate/.test(k);
    });
    autoKeys.forEach(function(k) { try { delete window[k]; } catch(e) {} });
    // Intercept future assignments
    var origDefineProperty = Object.defineProperty;
    Object.defineProperty = function(obj, prop, desc) {
      if (obj === window && /^cdc_|^__playwright|^__pw_|^__selenium|^_Selenium_IDE_Recorder|^__webdriver/.test(prop)) return obj;
      return origDefineProperty.call(Object, obj, prop, desc);
    };
  } catch(e) {}

  // §17 — Connection API aliases (mozConnection, webkitConnection)
  try {
    if (navigator.connection) {
      try { Object.defineProperty(navigator, 'mozConnection',    { get: function() { return navigator.connection; } }); } catch(e2) {}
      try { Object.defineProperty(navigator, 'webkitConnection', { get: function() { return navigator.connection; } }); } catch(e2) {}
    }
  } catch(e) {}

  // §18 — Iframe contentWindow.chrome consistency
  try {
    var origHTMLIFrameElement = Object.getOwnPropertyDescriptor(HTMLIFrameElement.prototype, 'contentWindow');
    if (origHTMLIFrameElement && origHTMLIFrameElement.get) {
      Object.defineProperty(HTMLIFrameElement.prototype, 'contentWindow', {
        get: function() {
          var win = origHTMLIFrameElement.get.call(this);
          if (win && !win.chrome) {
            try { win.chrome = window.chrome; } catch(e2) {}
          }
          return win;
        }
      });
    }
  } catch(e) {}

})();
`
