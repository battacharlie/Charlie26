// Intercom – logica comune ad app tecnici e console di regia.
(() => {
  'use strict';

  const $ = (id) => document.getElementById(id);
  const isConsole = document.body.dataset.role === 'regia';
  const HOLD_MS = 350; // oltre questo tempo una pressione è "push-to-talk"

  const state = {
    name: '',
    role: isConsole ? 'regia' : 'tecnico',
    channel: '',
    channels: [],        // tutti i canali dell'evento
    me: null,            // { id, role, channels }
    peers: [],
    stream: null,        // microfono locale
    ws: null,
    pc: null,
    latched: new Set(),  // canali aperti con un tocco
    held: new Map(),     // canale -> pressioni in corso
    talk: new Set(),     // canali su cui sto parlando (latched ∪ held)
    listenOff: new Set(),
    volume: loadJSON('intercom.volume', {}),
    retry: 0,
    retryTimer: null,
    audioEls: new Map(),   // id partecipante -> <audio>
    trackOwner: new Map(), // id traccia ricevuta -> id partecipante
    speaking: new Set(),   // partecipanti con voce rilevata ora
    localLevel: 0,
    wakeLock: null,
    joined: false,
  };

  function loadJSON(key, def) {
    try { return JSON.parse(localStorage.getItem(key)) || def; } catch (_) { return def; }
  }
  function saveJSON(key, v) {
    try { localStorage.setItem(key, JSON.stringify(v)); } catch (_) {}
  }
  const chName = (id) => (state.channels.find((c) => c.id === id) || { name: id }).name;

  // ---------- Ingresso ----------
  const nameInput = $('nameInput');
  nameInput.value = loadJSON('intercom.name', isConsole ? 'Regia' : '');
  if (isConsole && !nameInput.value) nameInput.value = 'Regia';

  fetch('/api/info').then((r) => r.json()).then((info) => {
    state.channels = info.channels || [];
    const sel = $('channelInput');
    if (!sel) return;
    for (const c of state.channels) {
      const o = document.createElement('option');
      o.value = c.id;
      o.textContent = c.name;
      sel.appendChild(o);
    }
    const saved = loadJSON('intercom.join', {});
    if (saved.channel && state.channels.some((c) => c.id === saved.channel)) sel.value = saved.channel;
    if (saved.role) {
      const r = document.querySelector(`input[name=role][value="${saved.role}"]`);
      if (r) r.checked = true;
    }
    sel.addEventListener('change', updateRoleField);
    updateRoleField();
  }).catch(() => showJoinError('Server non raggiungibile. Ricarica la pagina.'));

  function updateRoleField() {
    const sel = $('channelInput');
    const main = state.channels.find((c) => c.main);
    const isMain = main && sel.value === main.id;
    $('roleField').classList.toggle('hidden', !!isMain);
    if (main) $('roleHint').textContent = `Il caporeparto parla anche nel canale ${main.name}.`;
  }

  $('joinBtn').addEventListener('click', join);
  nameInput.addEventListener('keydown', (e) => { if (e.key === 'Enter') join(); });

  async function join() {
    $('joinError').classList.add('hidden');
    state.name = nameInput.value.trim() || 'Senza nome';
    saveJSON('intercom.name', state.name);
    if (!isConsole) {
      state.channel = $('channelInput').value;
      const r = document.querySelector('input[name=role]:checked');
      state.role = $('roleField').classList.contains('hidden') ? 'tecnico' : (r ? r.value : 'tecnico');
      saveJSON('intercom.join', { channel: state.channel, role: state.role });
    }

    if (!window.isSecureContext || !navigator.mediaDevices) {
      showJoinError('Questa pagina non è in HTTPS: il browser blocca il microfono. ' +
        'Apri l\'indirizzo che inizia con https:// (vedi la <a href="/aiuto">pagina di aiuto</a>).');
      return;
    }
    try {
      state.stream = state.stream || await navigator.mediaDevices.getUserMedia({
        audio: { echoCancellation: true, noiseSuppression: true, autoGainControl: true, channelCount: 1 },
        video: false,
      });
    } catch (e) {
      showJoinError('Microfono non disponibile (' + e.name + '). Controlla di aver premuto "Consenti". ' +
        'Su iPhone: Impostazioni › Safari › Microfono. Su Android: tocca il lucchetto nella barra degli indirizzi › Autorizzazioni.');
      return;
    }
    applyTalk(true);
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

  function backToJoin(message) {
    state.joined = false;
    clearTimeout(state.retryTimer);
    teardown();
    $('talkView').classList.add('hidden');
    $('joinView').classList.remove('hidden');
    showJoinError(message);
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
      send({ type: 'join', name: state.name, role: state.role, channel: state.channel });
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
          updateUI();
          applyVolumes();
          break;
        case 'error':
          if (!state.me) backToJoin(m.error); // rifiutato all'ingresso: inutile riprovare
          else banner(m.error, true);
          break;
      }
    };
    ws.onclose = () => {
      if (ws !== state.ws) return;
      setConn('bad', 'scollegato, riprovo…');
      scheduleReconnect();
    };
  }

  function onWelcome(m) {
    const prev = state.me && state.me.channels.join(',');
    state.me = { id: m.id, role: m.me.role, channels: m.me.channels };
    if (m.channels) state.channels = m.channels;
    // Canali non più validi (es. evento cambiato): li tolgo dagli stati locali.
    for (const set of [state.latched, state.listenOff]) {
      for (const c of [...set]) if (!state.me.channels.includes(c)) set.delete(c);
    }
    if (prev !== state.me.channels.join(',')) buildPanel();
    // Dopo una riconnessione il server riparte da zero: gli ripeto lo stato.
    for (const c of state.listenOff) send({ type: 'listen', channel: c, on: false });
    applyTalk(true);
    updateUI();
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
    if (state.talk.size && state.localLevel > 0.06 && state.me) now.add(state.me.id);
    const changed = now.size !== state.speaking.size || [...now].some((id) => !state.speaking.has(id));
    state.speaking = now;
    if (changed) updateUI();
  }, 160);

  // ---------- Pannello canali ----------
  // Costruito una volta sola (i pulsanti non vanno ricreati mentre li si preme);
  // updateUI aggiorna solo testi e colori.
  function buildPanel() {
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
        ptt.innerHTML = '<span>PARLA</span>' + (isConsole && i < 9 ? `<kbd>${i + 1}</kbd>` : '');
        bindTalk(ptt, [id]);
        ctrl.appendChild(ptt);
      }
      row.appendChild(ctrl);
      list.appendChild(row);
    });

    if (!multi) {
      // Un solo canale: il grande pulsante rotondo.
      const btn = document.createElement('button');
      btn.className = 'mic';
      btn.dataset.chs = mine[0];
      btn.innerHTML = micIcon + '<span class="lbl">MUTO</span>';
      bindTalk(btn, mine);
      big.appendChild(btn);
      const hint = document.createElement('div');
      hint.className = 'hint';
      hint.textContent = 'Tocca per aprire/chiudere · Tieni premuto per parlare';
      big.appendChild(hint);
    } else if (state.me.role === 'regia') {
      const all = document.createElement('button');
      all.className = 'ptt ptt-all';
      all.dataset.chs = mine.join(',');
      all.innerHTML = '<span>PARLA A TUTTI</span>' + (isConsole ? '<kbd>Spazio</kbd>' : '');
      bindTalk(all, mine);
      big.appendChild(all);
    }
  }

  const micIcon = '<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M12 14a3 3 0 0 0 3-3V5a3 3 0 0 0-6 0v6a3 3 0 0 0 3 3zm5-3a5 5 0 0 1-10 0H5a7 7 0 0 0 6 6.92V21h2v-3.08A7 7 0 0 0 19 11h-2z"/></svg>';
  const roleLabel = (r) => ({ regia: 'Regia', caporeparto: 'Caporeparto', tecnico: 'Tecnico' }[r] || r);

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
      const talkers = byChannel.get(id) || [];
      const others = talkers.filter((p) => p.id !== state.me.id);
      const who = row.querySelector('.chan-who');
      who.textContent = others.length ? others.map((p) => p.name).join(', ') : '';
      row.classList.toggle('busy', others.length > 0);
      row.classList.toggle('voice', others.some((p) => state.speaking.has(p.id)));
      row.classList.toggle('mine', state.talk.has(id));
      row.classList.toggle('off', state.listenOff.has(id));
      const listen = row.querySelector('.listen');
      listen.textContent = state.listenOff.has(id) ? '🔇' : '🔊';
      listen.setAttribute('aria-pressed', String(!state.listenOff.has(id)));
    });
    document.querySelectorAll('[data-chs]').forEach((btn) => {
      const chs = btn.dataset.chs.split(',');
      const on = chs.every((c) => state.talk.has(c));
      btn.classList.toggle('on', on);
      btn.setAttribute('aria-pressed', String(on));
      const lbl = btn.querySelector('.lbl');
      if (lbl) lbl.textContent = on ? 'IN ONDA' : 'MUTO';
    });
    renderPeers();
  }

  function renderPeers() {
    const ul = $('peerList');
    ul.textContent = '';
    $('peerCount').textContent = String(state.peers.length);
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
      ul.appendChild(li);
    }
  }

  // ---------- Parla: tocco = apri/chiudi, pressione lunga = parla finché tieni ----------
  const presses = new Map(); // chiave pressione -> { at, chs, wasOn }

  function pressStart(key, chs) {
    if (presses.has(key)) return;
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
    if (p.wasOn) {
      // era aperto: qualsiasi pressione lo chiude (già tolto da latched)
    } else if (tap) {
      for (const c of p.chs) state.latched.add(c); // tocco breve: resta aperto
    }
    applyTalk();
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
    if (same && !force) return;
    const wasTalking = state.talk.size > 0;
    state.talk = next;
    if (state.stream) state.stream.getAudioTracks().forEach((t) => { t.enabled = next.size > 0; });
    send({ type: 'talk', channels: [...next] });
    if (!force && navigator.vibrate && wasTalking !== next.size > 0) navigator.vibrate(next.size ? 25 : [10, 40, 10]);
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
      if (!state.me || e.target.tagName === 'INPUT') return null;
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
        state.localLevel = state.talk.size ? level : 0;
        document.querySelectorAll('.mic, .ptt').forEach((b) => b.style.setProperty('--level', b.classList.contains('on') ? state.localLevel.toFixed(3) : '0'));
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
    b.onclick = tapToPlay ? () => { state.audioEls.forEach((el) => el.play().catch(() => {})); hideBanner(); } : null;
  }
  function hideBanner() { $('talkBanner').classList.add('hidden'); }
})();
