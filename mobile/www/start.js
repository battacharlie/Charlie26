// Pagina iniziale dell'app: legge il QR dell'evento con la fotocamera e apre
// l'intercom. Il QR porta l'impronta del certificato dell'evento (#ca=…):
// la parte nativa (window.charlieApp) la usa per fidarsi solo di quel server.
(() => {
  'use strict';
  const $ = (id) => document.getElementById(id);
  const native = window.charlieApp;
  const KEY = 'charlie26.recenti';

  function loadRecent() {
    try { return JSON.parse(localStorage.getItem(KEY)) || []; } catch (_) { return []; }
  }
  function saveRecent(list) {
    try { localStorage.setItem(KEY, JSON.stringify(list.slice(0, 6))); } catch (_) {}
  }

  function showError(text) {
    const b = $('err');
    b.textContent = text;
    b.classList.toggle('hidden', !text);
    $('scanner').classList.remove('found');
    scanning = true;
  }
  // La parte nativa segnala qui se il certificato non corrisponde.
  window.startError = showError;

  // Interpreta il contenuto del QR: solo indirizzi https di un intercom CHARLIE26.
  function parse(text) {
    let u;
    try { u = new URL(text.trim()); } catch (_) { return null; }
    if (u.protocol !== 'https:') return null;
    const frag = new URLSearchParams(u.hash.slice(1));
    const regia = u.pathname.startsWith('/regia');
    return { url: u.href, base: u.origin + '/', pin: frag.get('ca') || '', regia, pairing: frag.has('abbina'), host: u.host };
  }

  function open(item, remember) {
    $('err').classList.add('hidden');
    if (remember) {
      const list = loadRecent().filter((r) => !(r.base === item.base && r.regia === item.regia));
      list.unshift({ base: item.base, pin: item.pin, regia: item.regia, host: item.host, at: Date.now() });
      saveRecent(list);
    }
    // Riaprendo non si ripete il codice di abbinamento (vale una volta sola): basta /regia.
    const target = item.reopen ? item.base + (item.regia ? 'regia' : '') : item.url;
    stopCamera();
    if (native && native.open) native.open(target, item.pin || '');
    else location.href = target;
  }

  // ---------- Elenco "Riapri" ----------
  function renderRecent() {
    const list = loadRecent();
    $('recentBox').classList.toggle('hidden', !list.length);
    const box = $('recent');
    box.textContent = '';
    for (const r of list) {
      const b = document.createElement('div');
      b.className = 'recent-item';
      b.innerHTML = '<span class="ic"></span><span class="t"><b></b><span></span></span><button class="x" title="Togli dall\'elenco">✕</button>';
      b.querySelector('.ic').textContent = r.regia ? '🎛' : '🎧';
      b.querySelector('b').textContent = r.regia ? 'Regia' : 'Intercom tecnici';
      b.querySelector('.t span').textContent = r.host + ' · ' + new Date(r.at).toLocaleDateString('it-IT');
      b.addEventListener('click', () => open({ ...r, reopen: true }, true));
      b.querySelector('.x').addEventListener('click', (e) => {
        e.stopPropagation();
        saveRecent(loadRecent().filter((x) => !(x.base === r.base && x.regia === r.regia)));
        renderRecent();
      });
      box.appendChild(b);
    }
  }

  // ---------- Fotocamera e lettura QR ----------
  let stream = null;
  let scanning = true;
  async function startCamera() {
    const msg = $('scanMsg');
    try {
      stream = await navigator.mediaDevices.getUserMedia({ video: { facingMode: 'environment', width: { ideal: 1280 }, height: { ideal: 720 } }, audio: false });
    } catch (e) {
      msg.textContent = 'Fotocamera non disponibile: consenti l\'accesso nelle impostazioni dell\'app.';
      return;
    }
    const v = $('cam');
    v.srcObject = stream;
    await v.play().catch(() => {});
    msg.textContent = 'Inquadra il QR dell\'evento';
    const canvas = $('grab');
    const ctx = canvas.getContext('2d', { willReadFrequently: true });
    const tick = () => {
      if (!stream) return;
      if (scanning && v.videoWidth) {
        // basta il centro dell'immagine, ridotto: più veloce
        const side = Math.min(v.videoWidth, v.videoHeight);
        const size = Math.min(640, side);
        canvas.width = size;
        canvas.height = size;
        ctx.drawImage(v, (v.videoWidth - side) / 2, (v.videoHeight - side) / 2, side, side, 0, 0, size, size);
        const img = ctx.getImageData(0, 0, size, size);
        const code = window.jsQR(img.data, size, size, { inversionAttempts: 'dontInvert' });
        if (code && code.data) found(code.data);
      }
      setTimeout(tick, 180);
    };
    tick();
  }
  function stopCamera() {
    if (stream) stream.getTracks().forEach((t) => t.stop());
    stream = null;
  }

  function found(text) {
    const item = parse(text);
    if (!item) {
      showError('Questo QR non è di un intercom CHARLIE26.');
      setTimeout(() => $('err').classList.add('hidden'), 3000);
      return;
    }
    scanning = false;
    $('scanner').classList.add('found');
    $('scanMsg').textContent = item.pairing ? 'Abbinamento come regia…' : 'Collegamento…';
    if (navigator.vibrate) navigator.vibrate(40);
    open(item, true);
  }

  $('manualForm').addEventListener('submit', (e) => {
    e.preventDefault();
    const item = parse($('manualUrl').value);
    if (!item) { showError('Scrivi un indirizzo che inizia con https://'); return; }
    open(item, true);
  });

  $('site').addEventListener('click', (e) => {
    if (native && native.openExternal) { e.preventDefault(); native.openExternal('https://www.andreabattaglioli.it'); }
  });

  document.addEventListener('visibilitychange', () => {
    if (document.visibilityState === 'visible' && !stream) { scanning = true; $('scanner').classList.remove('found'); startCamera(); }
    if (document.visibilityState === 'hidden') stopCamera();
  });

  renderRecent();
  startCamera();
})();
