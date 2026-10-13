// Intercom – logica comune ad app tecnici e console di regia.
(() => {
  'use strict';

  const $ = (id) => document.getElementById(id);
  const isConsole = document.body.dataset.role === 'regia';
  const HOLD_MS = 350; // oltre questo tempo una pressione è "push-to-talk"
  const CREATE = '__nuovo__';

  const state = {
    name: '',
    mode: '',            // 'libero' | 'credenziali'
    joinReq: null,       // dati di ingresso, ripetuti a ogni riconnessione
    channels: [],        // tutti i canali dell'evento
    me: null,            // { id, role, channels, admin, forced }
    peers: [],
    stream: null,        // microfono locale
    ws: null,
    pc: null,
    latched: new Set(),  // canali aperti con un tocco
    held: new Map(),     // canale -> pressioni in corso
    talk: new Set(),     // canali su cui sto parlando (latched ∪ held)
    listenOff: new Set(),
    volume: loadJSON('intercom.volume', {}),
    openAdmin: new Set(),  // canali con il pannello del responsabile aperto
    retry: 0,
    retryTimer: null,
    audioEls: new Map(),   // id partecipante -> <audio>
    trackOwner: new Map(), // id traccia ricevuta -> id partecipante
    speaking: new Set(),   // partecipanti con voce rilevata ora
    localLevel: 0,
    noise: loadJSON('intercom.rumore', false), // riduzione rumore forte (RNNoise)
    nsChain: null,       // { ctx, track } catena RNNoise attiva
    sender: null,        // RTCRtpSender del microfono
    wakeLock: null,
    joined: false,
    rebuildPending: false,
  };

  function loadJSON(key, def) {
    try { return JSON.parse(localStorage.getItem(key)) || def; } catch (_) { return def; }
  }
  function saveJSON(key, v) {
    try { localStorage.setItem(key, JSON.stringify(v)); } catch (_) {}
  }
  const chName = (id) => (state.channels.find((c) => c.id === id) || { name: id }).name;
  const chInfo = (id) => state.channels.find((c) => c.id === id) || {};
  // Gettoni da caporeparto, conservati sul telefono (libero accesso).
  const ownerTokens = () => loadJSON('intercom.owner', {});
  const roleLabel = (r) => ({ regia: 'Regia', caporeparto: 'Caporeparto', tecnico: 'Tecnico' }[r] || r);
  const micModeLabel = { libero: 'Libero', ptt: 'Solo push-to-talk', admin: 'Solo il caporeparto apre' };

  // ---------- Ingresso ----------
  const nameInput = $('nameInput');
  if (nameInput) nameInput.value = loadJSON('intercom.name', isConsole ? 'Regia' : '');
  if (isConsole && !nameInput.value) nameInput.value = 'Regia';

  function loadInfo() {
    return fetch('/api/info').then((r) => r.json()).then((info) => {
      state.channels = info.channels || [];
      state.mode = info.mode;
      if (!isConsole) prepareJoinForm();
    }).catch(() => showJoinError('Server non raggiungibile. Ricarica la pagina.'));
  }
  loadInfo();

  function prepareJoinForm() {
    const cred = state.mode === 'credenziali';
    $('freeForm').classList.toggle('hidden', cred);
    $('loginForm').classList.toggle('hidden', !cred);
    if (cred) {
      $('userInput').value = loadJSON('intercom.user', '');
      return;
    }
    const sel = $('channelInput');
    sel.textContent = '';
    for (const c of state.channels) {
      const o = document.createElement('option');
      o.value = c.id;
      o.textContent = c.name + (c.protected ? ' 🔒' : '');
      sel.appendChild(o);
    }
    const o = document.createElement('option');
    o.value = CREATE;
    o.textContent = '➕ Crea un nuovo canale…';
    sel.appendChild(o);
    const saved = loadJSON('intercom.join', {});
    if (saved.channel && state.channels.some((c) => c.id === saved.channel)) sel.value = saved.channel;
    sel.onchange = updateFreeFields;
    updateFreeFields();
  }

  function updateFreeFields() {
    const v = $('channelInput').value;
    const create = v === CREATE;
    const owned = !!ownerTokens()[v];
    $('createFields').classList.toggle('hidden', !create);
    $('passwordField').classList.toggle('hidden', create || !chInfo(v).protected || owned);
  }

  $('joinBtn').addEventListener('click', join);
  document.querySelectorAll('#joinView input').forEach((i) => i.addEventListener('keydown', (e) => { if (e.key === 'Enter') join(); }));

  async function join() {
    $('joinError').classList.add('hidden');
    let req;
    if (isConsole) {
      state.name = nameInput.value.trim() || 'Regia';
      saveJSON('intercom.name', state.name);
      req = { regia: true, name: state.name };
    } else if (state.mode === 'credenziali') {
      const username = $('userInput').value.trim();
      const password = $('userPassword').value;
      if (!username || !password) return showJoinError('Scrivi nome utente e password.');
      try {
        const r = await fetch('/api/login', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ username, password }) });
        const out = await r.json();
        if (!r.ok) return showJoinError(out.error || 'Accesso non riuscito.');
        saveJSON('intercom.user', username);
        $('userPassword').value = '';
        req = { session: out.session };
      } catch (_) {
        return showJoinError('Server non raggiungibile.');
      }
    } else {
      state.name = nameInput.value.trim();
      if (!state.name) return showJoinError('Scrivi il tuo nome.');
      saveJSON('intercom.name', state.name);
      const ch = $('channelInput').value;
      if (ch === CREATE) {
        const cname = $('createName').value.trim();
        if (!cname) return showJoinError('Scrivi il nome del nuovo canale.');
        req = { name: state.name, createName: cname, createPassword: $('createPassword').value };
      } else {
        req = { name: state.name, channel: ch, password: $('passwordInput').value };
        saveJSON('intercom.join', { channel: ch });
      }
    }
    state.joinReq = req;

    if (!window.isSecureContext || !navigator.mediaDevices) {
      return showJoinError('Questa pagina non è in HTTPS: il browser blocca il microfono. ' +
        'Apri l\'indirizzo che inizia con https:// (vedi la <a href="/aiuto">pagina di aiuto</a>).');
    }
    try {
      state.stream = state.stream || await navigator.mediaDevices.getUserMedia({
        audio: { echoCancellation: true, noiseSuppression: true, autoGainControl: true, channelCount: 1 },
        video: false,
      });
    } catch (e) {
      return showJoinError('Microfono non disponibile (' + e.name + '). Controlla di aver premuto "Consenti". ' +
        'Su iPhone: Impostazioni › Safari › Microfono. Su Android: tocca il lucchetto nella barra degli indirizzi › Autorizzazioni.');
    }
    addNoiseSwitch();
    if (state.noise) await setNoise(true);
    state.latched.clear();
    state.held.clear();
    applyTalk(true);
    startLevelMeter();
    state.me = null;
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

  function backToJoin(message) {
    state.joined = false;
    state.me = null;
    clearTimeout(state.retryTimer);
    teardown();
    for (const t of micTracks()) t.enabled = false;
    $('talkView').classList.add('hidden');
    $('joinView').classList.remove('hidden');
    $('channelList').textContent = '';
    $('bigMicArea').textContent = '';
    if (message) showJoinError(message);
    loadInfo(); // i canali potrebbero essere cambiati
  }

  const exitBtn = $('exitBtn');
  if (exitBtn) exitBtn.addEventListener('click', () => { if (confirm('Uscire dall\'intercom?')) backToJoin(''); });

  // ---------- Collegamento al server ----------
  function connect() {
    clearTimeout(state.retryTimer);
    teardown();
    setConn('wait', 'connessione…');

    const ws = new WebSocket(`wss://${location.host}/ws`);
    const pc = new RTCPeerConnection({ iceServers: [] }); // solo rete locale
    state.ws = ws;
    state.pc = pc;

    state.sender = pc.addTrack(sendTrack(), state.stream);

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
      send(Object.assign({ type: 'join', ownerTokens: Object.values(ownerTokens()) }, state.joinReq));
    };
    ws.onmessage = async (ev) => {
      if (ws !== state.ws) return;
      const m = JSON.parse(ev.data);
      switch (m.type) {
        case 'welcome':
          onWelcome(m);
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
          syncMe();
          updateUI();
          applyVolumes();
          break;
        case 'me':
          setMe(m.me);
          break;
        case 'channels':
          state.channels = m.channels || [];
          state.mode = m.mode;
          requestRebuild();
          break;
        case 'mute':
          // Il responsabile ha chiuso il mio microfono su questo canale.
          state.latched.delete(m.channel);
          state.held.delete(m.channel);
          applyTalk();
          banner('Il caporeparto ha chiuso il tuo microfono su ' + chName(m.channel) + '.', false);
          break;
        case 'ownerToken':
          storeOwnerToken(m.channel, m.token);
          banner('Sei stato nominato caporeparto di ' + chName(m.channel) + '.', false);
          break;
        case 'ownerRevoked':
          // La regia mi ha tolto il ruolo di caporeparto: i gettoni salvati non valgono più.
          saveJSON('intercom.owner', {});
          banner('La regia ti ha tolto il ruolo di caporeparto.', false);
          break;
        case 'kicked':
          backToJoin(m.error);
          break;
        case 'error':
          if (!state.me) {
            backToJoin(m.error); // rifiutato all'ingresso: inutile riprovare
          } else {
            banner(m.error, true);
          }
          break;
      }
    };
    ws.onclose = () => {
      if (ws !== state.ws) return;
      setConn('bad', 'scollegato, riprovo…');
      scheduleReconnect();
    };
  }

  function storeOwnerToken(channel, token) {
    const t = ownerTokens();
    t[channel] = token;
    saveJSON('intercom.owner', t);
  }

  function onWelcome(m) {
    if (m.channels) state.channels = m.channels;
    if (m.mode) state.mode = m.mode;
    if (m.token && m.channel) {
      // Ho creato un canale: da ora in poi rientro direttamente lì.
      storeOwnerToken(m.channel, m.token);
      state.joinReq = { name: state.joinReq.name, channel: m.channel };
      saveJSON('intercom.join', { channel: m.channel });
    }
    state.name = m.me.name;
    setMe(Object.assign({ id: m.id }, m.me), true);
    // Dopo una riconnessione il server riparte da zero: gli ripeto lo stato.
    for (const c of state.listenOff) send({ type: 'listen', channel: c, on: false });
    applyTalk(true);
  }

  function setMe(me, force) {
    const key = (x) => x ? [x.role, (x.channels || []).join(','), (x.admin || []).join(',')].join('|') : '';
    const prev = key(state.me);
    state.me = Object.assign({}, state.me, me);
    state.name = state.me.name || state.name;
    // Canali non più validi (es. evento cambiato): li tolgo dagli stati locali.
    for (const set of [state.latched, state.listenOff]) {
      for (const c of [...set]) if (!state.me.channels.includes(c)) set.delete(c);
    }
    if (force || prev !== key(state.me)) requestRebuild();
    else updateUI();
  }

  // Il mio stato come lo vede il server (es. microfono aperto dal caporeparto).
  function syncMe() {
    if (!state.me) return;
    const mine = state.peers.find((p) => p.id === state.me.id);
    if (!mine) return;
    const forcedBefore = (state.me.forced || []).join(',');
    state.me.forced = mine.forced || [];
    if (forcedBefore !== state.me.forced.join(',')) applyTalk(true);
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
    el.play().catch(() => banner('Tocca qui per attivare l\'audio.', false, true));
    applyVolumes();
    track.onended = () => removeRemote(owner);
    if (stream) stream.onremovetrack = () => { if (stream.getTracks().length === 0) removeRemote(owner); };
  }

  function removeRemote(owner) {
    const el = state.audioEls.get(owner);
    if (el) { el.remove(); state.audioEls.delete(owner); }
  }

  // Volume per canale: ogni voce prende il volume più alto tra i canali
  // su cui sta parlando e che io ascolto. (Su iPhone il browser ignora
  // il volume delle pagine: lì vale solo il tasto volume del telefono.)
  function applyVolumes() {
    for (const p of state.peers) {
      const el = state.audioEls.get(p.id);
      if (!el) continue;
      let v = 0;
      for (const c of p.talk || []) {
        if (state.me && state.me.channels.includes(c) && !state.listenOff.has(c)) v = Math.max(v, volumeOf(c));
      }
      el.volume = v || 1;
    }
  }
  const volumeOf = (c) => (typeof state.volume[c] === 'number' ? state.volume[c] : 1);

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
    if (micOpen() && state.localLevel > 0.06 && state.me) now.add(state.me.id);
    const changed = now.size !== state.speaking.size || [...now].some((id) => !state.speaking.has(id));
    state.speaking = now;
    if (changed) updateUI();
  }, 160);

  // ---------- Permessi del microfono ----------
  const isAdminOf = (c) => !!state.me && (state.me.admin || []).includes(c);
  const isForced = (c) => !!state.me && (state.me.forced || []).includes(c);
  // Come posso usare il microfono su questo canale: 'libero', 'ptt' o 'admin' (solo il responsabile).
  const myMicMode = (c) => (isAdminOf(c) ? 'libero' : chInfo(c).micMode || 'libero');
  const micOpen = () => state.talk.size > 0 || (state.me && (state.me.forced || []).length > 0);

  // ---------- Pannello canali ----------
  // Costruito solo quando cambiano canali o permessi (i pulsanti non vanno
  // ricreati mentre li si preme); updateUI aggiorna solo testi e colori.
  function requestRebuild() {
    if (presses.size) { state.rebuildPending = true; return; }
    buildPanel();
  }

  function buildPanel() {
    state.rebuildPending = false;
    if (!state.me) return;
    const list = $('channelList');
    const big = $('bigMicArea');
    list.textContent = '';
    big.textContent = '';
    const mine = state.me.channels;
    const multi = mine.length > 1;
    $('meLabel').textContent = state.name + ' · ' + roleLabel(state.me.role);

    mine.forEach((id, i) => {
      const row = document.createElement('div');
      row.className = 'chan';
      row.dataset.ch = id;

      const head = document.createElement('div');
      head.className = 'chan-head';
      head.innerHTML = '<span class="chan-name"></span><span class="chan-who"></span>';
      head.querySelector('.chan-name').textContent = chName(id);
      if (isAdminOf(id) && (!chInfo(id).main || state.me.role === 'regia')) {
        const gear = document.createElement('button');
        gear.className = 'gear';
        gear.title = 'Gestisci il canale';
        gear.textContent = '⚙';
        gear.addEventListener('click', () => {
          if (state.openAdmin.has(id)) state.openAdmin.delete(id); else state.openAdmin.add(id);
          updateUI();
        });
        head.appendChild(gear);
      }
      row.appendChild(head);

      const ctrl = document.createElement('div');
      ctrl.className = 'chan-ctrl';
      const listen = document.createElement('button');
      listen.className = 'listen';
      listen.title = 'Ascolto del canale';
      listen.addEventListener('click', () => toggleListen(id));
      ctrl.appendChild(listen);

      if (multi) {
        const vol = document.createElement('input');
        vol.type = 'range';
        vol.min = '0';
        vol.max = '1';
        vol.step = '0.05';
        vol.value = String(volumeOf(id));
        vol.className = 'vol';
        vol.title = 'Volume del canale';
        vol.addEventListener('input', () => {
          state.volume[id] = Number(vol.value);
          saveJSON('intercom.volume', state.volume);
          applyVolumes();
        });
        ctrl.appendChild(vol);

        const ptt = document.createElement('button');
        ptt.className = 'ptt';
        ptt.dataset.chs = id;
        ptt.innerHTML = '<span class="lbl">PARLA</span>' + (isConsole && i < 9 ? `<kbd>${i + 1}</kbd>` : '');
        bindTalk(ptt, [id]);
        ctrl.appendChild(ptt);
      }
      row.appendChild(ctrl);

      if (isAdminOf(id)) row.appendChild(buildAdminPanel(id));
      list.appendChild(row);
    });

    if (!multi && mine.length) {
      // Un solo canale: il grande pulsante rotondo.
      const btn = document.createElement('button');
      btn.className = 'mic';
      btn.dataset.chs = mine[0];
      btn.innerHTML = micIcon + '<span class="lbl">MUTO</span>';
      bindTalk(btn, mine);
      big.appendChild(btn);
      const hint = document.createElement('div');
      hint.className = 'hint';
      hint.id = 'micHint';
      big.appendChild(hint);
    } else if (state.me.role === 'regia') {
      const all = document.createElement('button');
      all.className = 'ptt ptt-all';
      all.dataset.chs = mine.join(',');
      all.innerHTML = '<span>PARLA A TUTTI</span>' + (isConsole ? '<kbd>Spazio</kbd>' : '');
      bindTalk(all, mine);
      big.appendChild(all);
    }
    updateUI();
  }

  // Pannello del responsabile: gestione del microfono degli utenti, video,
  // apertura/chiusura del microfono dei singoli.
  function buildAdminPanel(id) {
    const box = document.createElement('div');
    box.className = 'admin-panel';

    const modeLbl = document.createElement('div');
    modeLbl.className = 'small muted';
    modeLbl.textContent = 'Microfono degli utenti';
    box.appendChild(modeLbl);
    const seg = document.createElement('div');
    seg.className = 'seg small-seg';
    for (const mode of ['libero', 'ptt', 'admin']) {
      const b = document.createElement('button');
      b.dataset.mode = mode;
      b.textContent = micModeLabel[mode];
      b.addEventListener('click', () => send({ type: 'channelSettings', channel: id, micMode: mode, video: !!chInfo(id).video }));
      seg.appendChild(b);
    }
    box.appendChild(seg);

    const video = document.createElement('label');
    video.className = 'switch';
    video.innerHTML = '<input type="checkbox"><span>Video della regia in questo canale</span>';
    video.querySelector('input').addEventListener('change', (e) => {
      send({ type: 'channelSettings', channel: id, micMode: chInfo(id).micMode, video: e.target.checked });
    });
    box.appendChild(video);

    const users = document.createElement('ul');
    users.className = 'members';
    box.appendChild(users);
    return box;
  }

  const micIcon = '<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M12 14a3 3 0 0 0 3-3V5a3 3 0 0 0-6 0v6a3 3 0 0 0 3 3zm5-3a5 5 0 0 1-10 0H5a7 7 0 0 0 6 6.92V21h2v-3.08A7 7 0 0 0 19 11h-2z"/></svg>';

  function toggleListen(id) {
    const off = !state.listenOff.has(id);
    if (off) state.listenOff.add(id); else state.listenOff.delete(id);
    send({ type: 'listen', channel: id, on: !off });
    updateUI();
    applyVolumes();
  }

  function updateUI() {
    if (!state.me) return;
    const byChannel = new Map();
    for (const p of state.peers) {
      for (const c of p.talk || []) {
        if (!byChannel.has(c)) byChannel.set(c, []);
        byChannel.get(c).push(p);
      }
    }
    document.querySelectorAll('#channelList .chan').forEach((row) => {
      const id = row.dataset.ch;
      const others = (byChannel.get(id) || []).filter((p) => p.id !== state.me.id);
      row.querySelector('.chan-who').textContent = others.map((p) => p.name).join(', ');
      row.classList.toggle('busy', others.length > 0);
      row.classList.toggle('voice', others.some((p) => state.speaking.has(p.id)));
      row.classList.toggle('mine', state.talk.has(id) || isForced(id));
      row.classList.toggle('off', state.listenOff.has(id));
      const listen = row.querySelector('.listen');
      listen.textContent = state.listenOff.has(id) ? '🔇' : '🔊';
      listen.setAttribute('aria-pressed', String(!state.listenOff.has(id)));
      const panel = row.querySelector('.admin-panel');
      if (panel) updateAdminPanel(panel, id);
    });
    document.querySelectorAll('[data-chs]').forEach((btn) => {
      const chs = btn.dataset.chs.split(',');
      const forced = chs.every(isForced);
      const on = chs.every((c) => state.talk.has(c) || isForced(c));
      const locked = chs.every((c) => myMicMode(c) === 'admin');
      btn.classList.toggle('on', on);
      btn.classList.toggle('locked', locked && !on);
      btn.setAttribute('aria-pressed', String(on));
      const lbl = btn.querySelector('.lbl');
      if (lbl) {
        const big = btn.classList.contains('mic');
        lbl.textContent = forced ? 'APERTO DAL CAPOREPARTO' : on ? 'IN ONDA' : locked ? 'GESTITO DAL CAPO' : big ? 'MUTO' : 'PARLA';
      }
    });
    const hint = $('micHint');
    if (hint && state.me.channels.length === 1) {
      hint.textContent = { libero: 'Tocca per aprire/chiudere · Tieni premuto per parlare', ptt: 'Tieni premuto per parlare', admin: 'Il microfono lo apre il caporeparto' }[myMicMode(state.me.channels[0])];
    }
    renderPeers();
  }

  function updateAdminPanel(panel, id) {
    panel.classList.toggle('hidden', !state.openAdmin.has(id));
    const info = chInfo(id);
    panel.querySelectorAll('[data-mode]').forEach((b) => b.classList.toggle('sel', b.dataset.mode === info.micMode));
    const vid = panel.querySelector('.switch input');
    if (vid) vid.checked = !!info.video;
    const ul = panel.querySelector('.members');
    ul.textContent = '';
    const members = state.peers.filter((p) => p.id !== state.me.id && (p.channels || []).includes(id) && (!(p.admin || []).includes(id) || state.me.role === 'regia') && p.role !== 'regia');
    if (!members.length) {
      const li = document.createElement('li');
      li.className = 'muted small';
      li.textContent = 'Nessun utente collegato a questo canale.';
      ul.appendChild(li);
    }
    for (const p of members) {
      const li = document.createElement('li');
      const open = (p.talk || []).includes(id);
      const name = document.createElement('span');
      name.textContent = p.name;
      name.className = 'name' + (state.speaking.has(p.id) ? ' talking' : '');
      const b = document.createElement('button');
      b.className = 'mini ' + (open ? 'danger' : '');
      b.textContent = open ? 'Chiudi mic' : 'Apri mic';
      b.addEventListener('click', () => send({ type: 'control', peer: p.id, channel: id, on: !open }));
      li.append(name, b);
      ul.appendChild(li);
    }
  }

  function renderPeers() {
    const ul = $('peerList');
    ul.textContent = '';
    $('peerCount').textContent = String(state.peers.length);
    const regia = state.me.role === 'regia';
    for (const p of state.peers) {
      const li = document.createElement('li');
      if (state.speaking.has(p.id)) li.classList.add('speaking');
      const name = document.createElement('span');
      name.className = 'name';
      name.textContent = p.name + (p.id === state.me.id ? ' (tu)' : '');
      const tag = document.createElement('span');
      tag.className = 'tag';
      tag.textContent = roleLabel(p.role) + ' · ' + (p.channels || []).map(chName).join(', ');
      const box = document.createElement('span');
      box.className = 'who';
      box.append(name, tag);
      li.appendChild(box);
      for (const c of p.talk || []) {
        const b = document.createElement('span');
        b.className = 'mic-badge on';
        b.textContent = chName(c);
        li.appendChild(b);
      }
      if (regia && p.id !== state.me.id) {
        const sub = (p.channels || []).find((c) => !chInfo(c).main);
        if (p.role === 'tecnico' && sub) {
          const pr = document.createElement('button');
          pr.className = 'mini';
          pr.textContent = 'Nomina caporeparto';
          pr.title = 'Diventa caporeparto di ' + chName(sub);
          pr.addEventListener('click', () => send({ type: 'promote', peer: p.id, channel: sub }));
          li.appendChild(pr);
        } else if (p.role === 'caporeparto') {
          const de = document.createElement('button');
          de.className = 'mini';
          de.textContent = 'Togli caporeparto';
          de.title = p.name + ' torna tecnico';
          de.addEventListener('click', () => { if (confirm('Togliere a ' + p.name + ' il ruolo di caporeparto?')) send({ type: 'demote', peer: p.id }); });
          li.appendChild(de);
        }
        const k = document.createElement('button');
        k.className = 'mini danger';
        k.textContent = 'Scollega';
        k.addEventListener('click', () => { if (confirm('Scollegare ' + p.name + '?')) send({ type: 'kick', peer: p.id }); });
        li.appendChild(k);
      }
      ul.appendChild(li);
    }
  }

  // ---------- Parla: tocco = apri/chiudi, pressione lunga = parla finché tieni ----------
  const presses = new Map(); // chiave pressione -> { at, chs, wasOn }

  function pressStart(key, allChs) {
    if (presses.has(key)) return;
    // Solo i canali dove posso aprirmi il microfono da solo.
    const chs = allChs.filter((c) => myMicMode(c) !== 'admin');
    if (!chs.length) {
      if (navigator.vibrate) navigator.vibrate([30, 60, 30]);
      return;
    }
    const wasOn = chs.every((c) => state.latched.has(c) || (state.held.get(c) || 0) > 0);
    presses.set(key, { at: performance.now(), chs, wasOn });
    for (const c of chs) state.held.set(c, (state.held.get(c) || 0) + 1);
    if (wasOn) for (const c of chs) state.latched.delete(c);
    applyTalk();
  }

  function pressEnd(key) {
    const p = presses.get(key);
    if (!p) return;
    presses.delete(key);
    for (const c of p.chs) state.held.set(c, Math.max(0, (state.held.get(c) || 0) - 1));
    const tap = performance.now() - p.at <= HOLD_MS;
    if (!p.wasOn && tap) {
      // Tocco breve: resta aperto, ma solo dove è permesso (non in "solo push-to-talk").
      for (const c of p.chs) if (myMicMode(c) === 'libero') state.latched.add(c);
    }
    applyTalk();
    if (!presses.size && state.rebuildPending) buildPanel();
  }

  // wasOn + pressione: il canale va chiuso, ma mentre si tiene premuto
  // l'held lo terrebbe aperto. Lo escludiamo finché dura la pressione.
  function closingByPress(c) {
    for (const p of presses.values()) if (p.wasOn && p.chs.includes(c)) return true;
    return false;
  }

  function applyTalk(force) {
    const next = new Set();
    for (const c of state.latched) next.add(c);
    for (const [c, n] of state.held) if (n > 0 && !closingByPress(c)) next.add(c);
    const same = next.size === state.talk.size && [...next].every((c) => state.talk.has(c));
    const wasOpen = micOpen();
    state.talk = next;
    for (const t of micTracks()) t.enabled = micOpen();
    if (same && !force) return;
    send({ type: 'talk', channels: [...next] });
    if (!force && navigator.vibrate && wasOpen !== micOpen()) navigator.vibrate(micOpen() ? 25 : [10, 40, 10]);
    updateUI();
  }

  let pressSeq = 0;
  function bindTalk(btn, chs) {
    btn.addEventListener('pointerdown', (e) => {
      e.preventDefault();
      btn.setPointerCapture(e.pointerId);
      btn.dataset.press = 'p' + (++pressSeq);
      pressStart(btn.dataset.press, chs);
    });
    const end = () => { if (btn.dataset.press) { pressEnd(btn.dataset.press); delete btn.dataset.press; } };
    btn.addEventListener('pointerup', end);
    btn.addEventListener('pointercancel', end);
    btn.addEventListener('contextmenu', (e) => e.preventDefault());
  }

  // Tastiera in console: 1–9 canali, Spazio = tutti.
  if (isConsole) {
    const keyChannels = (e) => {
      if (!state.me || ['INPUT', 'SELECT', 'TEXTAREA'].includes(e.target.tagName)) return null;
      const mine = state.me.channels;
      if (e.code === 'Space') return mine;
      const m = /^(Digit|Numpad)([1-9])$/.exec(e.code);
      if (m && mine[Number(m[2]) - 1]) return [mine[Number(m[2]) - 1]];
      return null;
    };
    document.addEventListener('keydown', (e) => {
      const chs = keyChannels(e);
      if (!chs) return;
      e.preventDefault();
      if (!e.repeat) pressStart('k' + e.code, chs);
    });
    document.addEventListener('keyup', (e) => {
      if (!keyChannels(e)) return;
      e.preventDefault();
      pressEnd('k' + e.code);
    });
    // Se la finestra perde il fuoco con un tasto premuto, rilascia tutto.
    window.addEventListener('blur', () => { for (const k of [...presses.keys()]) if (k[0] === 'k') pressEnd(k); });
  }

  // Misura del livello del proprio microfono: anello attorno al pulsante.
  let meterStarted = false;
  function startLevelMeter() {
    if (meterStarted) return;
    meterStarted = true;
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
        state.localLevel = micOpen() ? level : 0;
        document.querySelectorAll('.mic, .ptt').forEach((b) => b.style.setProperty('--level', b.classList.contains('on') ? state.localLevel.toFixed(3) : '0'));
        requestAnimationFrame(tick);
      };
      tick();
      document.addEventListener('visibilitychange', () => { if (ctx.state === 'suspended') ctx.resume(); });
    } catch (_) { /* non indispensabile */ }
  }

  // ---------- Riduzione rumore forte (RNNoise, lo stesso filtro di Jitsi) ----------
  const rawTrack = () => state.stream.getAudioTracks()[0];
  const sendTrack = () => (state.nsChain ? state.nsChain.track : rawTrack());
  const micTracks = () => (state.stream ? [rawTrack(), state.nsChain && state.nsChain.track].filter(Boolean) : []);

  function addNoiseSwitch() {
    if ($('nsToggle')) return;
    const label = document.createElement('label');
    label.className = 'switch';
    label.innerHTML = '<input type="checkbox" id="nsToggle"><span>Riduzione rumore forte</span><small class="muted">per ambienti rumorosi (palco, sala macchine)</small>';
    $('channelList').insertAdjacentElement('afterend', label);
    const box = $('nsToggle');
    box.checked = state.noise;
    box.addEventListener('change', async () => {
      const ok = await setNoise(box.checked);
      if (!ok) box.checked = false;
      state.noise = box.checked;
      saveJSON('intercom.rumore', state.noise);
    });
  }

  async function setNoise(on) {
    if (on && !state.nsChain) {
      let ctx;
      try {
        try { ctx = new AudioContext({ sampleRate: 48000 }); } catch (_) { ctx = new AudioContext(); }
        await ctx.audioWorklet.addModule('/static/rnnoise-worklet.js?v=1');
        const src = ctx.createMediaStreamSource(state.stream);
        const node = new AudioWorkletNode(ctx, 'rnnoise', { numberOfInputs: 1, numberOfOutputs: 1, outputChannelCount: [1] });
        const dest = ctx.createMediaStreamDestination();
        src.connect(node).connect(dest);
        if (ctx.state === 'suspended') await ctx.resume();
        state.nsChain = { ctx, track: dest.stream.getAudioTracks()[0] };
      } catch (e) {
        console.error('RNNoise', e);
        if (ctx) ctx.close().catch(() => {});
        banner('Riduzione rumore forte non disponibile su questo browser.', false);
        return false;
      }
    } else if (!on && state.nsChain) {
      const old = state.nsChain;
      state.nsChain = null;
      setTimeout(() => old.ctx.close().catch(() => {}), 500);
    }
    for (const t of micTracks()) t.enabled = micOpen();
    if (state.sender) await state.sender.replaceTrack(sendTrack()).catch(() => {});
    return true;
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
    if (kind === 'ok' && $('talkBanner').dataset.sticky !== '1') hideBanner();
  }
  let bannerTimer = null;
  function banner(text, isError, tapToPlay) {
    const b = $('talkBanner');
    b.textContent = text;
    b.classList.toggle('error', !!isError);
    b.classList.remove('hidden');
    b.dataset.sticky = tapToPlay ? '1' : '';
    b.onclick = tapToPlay ? () => { state.audioEls.forEach((el) => el.play().catch(() => {})); hideBanner(); } : hideBanner;
    clearTimeout(bannerTimer);
    if (!tapToPlay) bannerTimer = setTimeout(hideBanner, 6000);
  }
  function hideBanner() { const b = $('talkBanner'); b.classList.add('hidden'); b.dataset.sticky = ''; }
})();
