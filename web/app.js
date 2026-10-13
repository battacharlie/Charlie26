// Intercom – logica comune ad app tecnici e console di regia.
(() => {
  'use strict';

  const $ = (id) => document.getElementById(id);
  const isConsole = document.body.dataset.role === 'regia';

  const state = {
    name: '',
    myId: '',
    stream: null,      // microfono locale
    ws: null,
    pc: null,
    micOn: false,
    peers: [],
    retry: 0,
    retryTimer: null,
    audioEls: new Map(),   // id partecipante -> <audio>
    trackOwner: new Map(), // id traccia ricevuta -> id partecipante
    speaking: new Set(),
    wakeLock: null,
    joined: false,
  };

  // ---------- Ingresso ----------
  const nameInput = $('nameInput');
  try { nameInput.value = localStorage.getItem('intercom.name') || (isConsole ? 'Regia' : ''); } catch (_) {}

  $('joinBtn').addEventListener('click', join);
  nameInput.addEventListener('keydown', (e) => { if (e.key === 'Enter') join(); });

  async function join() {
    const err = $('joinError');
    err.classList.add('hidden');
    state.name = nameInput.value.trim() || 'Senza nome';
    try { localStorage.setItem('intercom.name', state.name); } catch (_) {}

    if (!window.isSecureContext || !navigator.mediaDevices) {
      showJoinError('Questa pagina non è in HTTPS: il browser blocca il microfono. ' +
        'Apri l\'indirizzo che inizia con https:// (vedi la <a href="/aiuto">pagina di aiuto</a>).');
      return;
    }
    try {
      state.stream = await navigator.mediaDevices.getUserMedia({
        audio: { echoCancellation: true, noiseSuppression: true, autoGainControl: true, channelCount: 1 },
        video: false,
      });
    } catch (e) {
      showJoinError('Microfono non disponibile (' + e.name + '). Controlla di aver premuto "Consenti". ' +
        'Su iPhone: Impostazioni › Safari › Microfono. Su Android: tocca il lucchetto nella barra degli indirizzi › Autorizzazioni.');
      return;
    }
    setMic(false);
    startLevelMeter();
    $('joinView').classList.add('hidden');
    $('talkView').classList.remove('hidden');
    state.joined = true;
    connect();
    keepScreenOn();
  }

  function showJoinError(html) {
    const err = $('joinError');
    err.innerHTML = html;
    err.classList.remove('hidden');
  }

  // ---------- Collegamento al server ----------
  function connect() {
    clearTimeout(state.retryTimer);
    teardown();
    setConn('wait', 'connessione…');

    const ws = new WebSocket(`wss://${location.host}/ws`);
    const pc = new RTCPeerConnection({ iceServers: [] }); // solo rete locale
    state.ws = ws;
    state.pc = pc;

    state.stream.getAudioTracks().forEach((t) => pc.addTrack(t, state.stream));

    pc.onicecandidate = (e) => { if (e.candidate) send({ type: 'candidate', candidate: e.candidate.toJSON() }); };
    pc.ontrack = (e) => attachRemote(e.track, e.streams[0]);
    pc.onconnectionstatechange = () => {
      if (pc !== state.pc) return;
      const s = pc.connectionState;
      if (s === 'connected') { state.retry = 0; setConn('ok', 'collegato'); }
      else if (s === 'connecting') setConn('wait', 'audio in collegamento…');
      else if (s === 'failed') { setConn('bad', 'audio non raggiungibile'); scheduleReconnect(); }
      else if (s === 'disconnected') setConn('wait', 'segnale perso, attendo…');
    };

    ws.onopen = () => {
      send({ type: 'join', name: state.name });
      if (state.micOn) send({ type: 'mic', on: true });
    };
    ws.onmessage = async (ev) => {
      if (ws !== state.ws) return;
      const m = JSON.parse(ev.data);
      switch (m.type) {
        case 'welcome':
          state.myId = m.id;
          break;
        case 'offer':
          try {
            await pc.setRemoteDescription({ type: 'offer', sdp: m.sdp });
            const answer = await pc.createAnswer();
            await pc.setLocalDescription(answer);
            send({ type: 'answer', sdp: pc.localDescription.sdp });
          } catch (e) {
            console.error('negoziazione', e);
            scheduleReconnect();
          }
          break;
        case 'peers':
          state.peers = m.peers || [];
          renderPeers();
          break;
        case 'error':
          banner(m.error, true);
          break;
      }
    };
    ws.onclose = () => {
      if (ws !== state.ws) return;
      setConn('bad', 'scollegato, riprovo…');
      scheduleReconnect();
    };
  }

  function send(m) {
    if (state.ws && state.ws.readyState === WebSocket.OPEN) state.ws.send(JSON.stringify(m));
  }

  function teardown() {
    const { ws, pc } = state;
    state.ws = null;
    state.pc = null;
    if (ws) { ws.onclose = null; try { ws.close(); } catch (_) {} }
    if (pc) { try { pc.close(); } catch (_) {} }
    state.audioEls.forEach((el) => el.remove());
    state.audioEls.clear();
    state.trackOwner.clear();
  }

  function scheduleReconnect() {
    if (!state.joined) return;
    clearTimeout(state.retryTimer);
    const delay = Math.min(5000, 500 * 2 ** state.retry++);
    state.retryTimer = setTimeout(connect, delay);
  }

  // Tornando sulla pagina (schermo riacceso, cambio app) ricontrolla il collegamento.
  document.addEventListener('visibilitychange', () => {
    if (document.visibilityState !== 'visible' || !state.joined) return;
    keepScreenOn();
    const ws = state.ws;
    if (!ws || ws.readyState > WebSocket.OPEN || (state.pc && state.pc.connectionState === 'failed')) connect();
  });
  setInterval(() => send({ type: 'ping' }), 15000);

  // ---------- Audio ricevuto ----------
  function attachRemote(track, stream) {
    const owner = stream ? stream.id : track.id;
    state.trackOwner.set(track.id, owner);
    if (track.kind !== 'audio') return; // il video arriverà nella Fase 5
    let el = state.audioEls.get(owner);
    if (!el) {
      el = document.createElement('audio');
      el.autoplay = true;
      el.setAttribute('playsinline', '');
      document.body.appendChild(el);
      state.audioEls.set(owner, el);
    }
    el.srcObject = stream || new MediaStream([track]);
    el.play().catch(() => banner('Tocca lo schermo per attivare l\'audio.', false, true));
    track.onended = () => removeRemote(owner);
    if (stream) stream.onremovetrack = () => { if (stream.getTracks().length === 0) removeRemote(owner); };
  }

  function removeRemote(owner) {
    const el = state.audioEls.get(owner);
    if (el) { el.remove(); state.audioEls.delete(owner); }
  }

  // Indicatore "sta parlando": livello audio dei flussi ricevuti, letto ~6 volte al secondo.
  setInterval(() => {
    const pc = state.pc;
    if (!pc) return;
    const now = new Set();
    for (const r of pc.getReceivers()) {
      if (!r.track || r.track.kind !== 'audio' || !r.getSynchronizationSources) continue;
      const src = r.getSynchronizationSources()[0];
      // timestamp: alcuni browser usano performance.now(), altri l'ora di sistema
      const age = src ? Math.min(Math.abs(performance.now() - src.timestamp), Math.abs(Date.now() - src.timestamp)) : Infinity;
      if (src && src.audioLevel > 0.02 && age < 1000) {
        const owner = state.trackOwner.get(r.track.id);
        if (owner) now.add(owner);
      }
    }
    if (state.micOn && state.localLevel > 0.06 && state.myId) now.add(state.myId);
    const changed = now.size !== state.speaking.size || [...now].some((id) => !state.speaking.has(id));
    state.speaking = now;
    if (changed) renderPeers();
  }, 160);

  // ---------- Lista partecipanti ----------
  function renderPeers() {
    const ul = $('peerList');
    ul.textContent = '';
    for (const p of state.peers) {
      const li = document.createElement('li');
      if (state.speaking.has(p.id)) li.classList.add('speaking');
      const name = document.createElement('span');
      name.className = 'name';
      name.textContent = p.name;
      li.appendChild(name);
      if (p.id === state.myId) {
        const tag = document.createElement('span');
        tag.className = 'tag';
        tag.textContent = 'tu';
        li.appendChild(tag);
      }
      const mic = document.createElement('span');
      mic.className = 'mic-badge' + (p.mic ? ' on' : '');
      mic.textContent = p.mic ? 'MIC' : 'muto';
      li.appendChild(mic);
      ul.appendChild(li);
    }
    if (state.peers.length <= 1) {
      const li = document.createElement('li');
      li.innerHTML = '<span class="name muted">Nessun altro collegato</span>';
      ul.appendChild(li);
    }
  }

  // ---------- Microfono: tocco = apri/chiudi, pressione lunga = parla finché tieni ----------
  const micBtn = $('micBtn');
  const HOLD_MS = 350;
  let press = null;

  function setMic(on) {
    state.micOn = on;
    if (state.stream) state.stream.getAudioTracks().forEach((t) => { t.enabled = on; });
    micBtn.classList.toggle('on', on);
    micBtn.setAttribute('aria-pressed', String(on));
    $('micLabel').textContent = on ? 'IN ONDA' : 'MUTO';
    send({ type: 'mic', on });
    if (navigator.vibrate) navigator.vibrate(on ? 25 : [10, 40, 10]);
  }

  function pressStart() {
    if (press) return;
    press = { at: performance.now(), wasOn: state.micOn };
    if (!state.micOn) setMic(true);
  }
  function pressEnd() {
    if (!press) return;
    const held = performance.now() - press.at;
    if (press.wasOn) setMic(false);          // era aperto: qualsiasi tocco lo chiude
    else if (held > HOLD_MS) setMic(false);  // push-to-talk: rilasciato, si chiude
    press = null;                            // tocco breve: resta aperto
  }
  micBtn.addEventListener('pointerdown', (e) => { e.preventDefault(); micBtn.setPointerCapture(e.pointerId); pressStart(); });
  micBtn.addEventListener('pointerup', pressEnd);
  micBtn.addEventListener('pointercancel', pressEnd);
  micBtn.addEventListener('contextmenu', (e) => e.preventDefault());

  // In console: barra spaziatrice come tasto parla.
  if (isConsole) {
    document.addEventListener('keydown', (e) => {
      if (e.code === 'Space' && !e.repeat && state.joined && e.target.tagName !== 'INPUT') { e.preventDefault(); pressStart(); }
    });
    document.addEventListener('keyup', (e) => {
      if (e.code === 'Space' && state.joined && e.target.tagName !== 'INPUT') { e.preventDefault(); pressEnd(); }
    });
  }

  // Misura del livello del proprio microfono: anello attorno al pulsante.
  function startLevelMeter() {
    try {
      const Ctx = window.AudioContext || window.webkitAudioContext;
      const ctx = new Ctx();
      const src = ctx.createMediaStreamSource(state.stream);
      const an = ctx.createAnalyser();
      an.fftSize = 512;
      src.connect(an);
      const buf = new Float32Array(an.fftSize);
      const tick = () => {
        an.getFloatTimeDomainData(buf);
        let sum = 0;
        for (const v of buf) sum += v * v;
        const level = Math.min(1, Math.sqrt(sum / buf.length) * 4);
        state.localLevel = state.micOn ? level : 0;
        micBtn.style.setProperty('--level', state.localLevel.toFixed(3));
        requestAnimationFrame(tick);
      };
      tick();
      document.addEventListener('visibilitychange', () => { if (ctx.state === 'suspended') ctx.resume(); });
    } catch (_) { /* non indispensabile */ }
  }

  // ---------- Schermo sempre acceso ----------
  async function keepScreenOn() {
    if (!('wakeLock' in navigator) || state.wakeLock) return;
    try {
      state.wakeLock = await navigator.wakeLock.request('screen');
      state.wakeLock.addEventListener('release', () => { state.wakeLock = null; });
    } catch (_) { /* es. risparmio energetico attivo */ }
  }

  // ---------- Interfaccia ----------
  function setConn(kind, text) {
    $('connDot').className = 'dot' + (kind === 'ok' ? ' ok' : kind === 'bad' ? ' bad' : '');
    $('connText').textContent = text;
    if (kind === 'ok') hideBanner();
  }
  function banner(text, isError, tapToPlay) {
    const b = $('talkBanner');
    b.textContent = text;
    b.classList.toggle('error', !!isError);
    b.classList.remove('hidden');
    if (tapToPlay) {
      b.onclick = () => { state.audioEls.forEach((el) => el.play().catch(() => {})); hideBanner(); };
    }
  }
  function hideBanner() { $('talkBanner').classList.add('hidden'); }
})();
