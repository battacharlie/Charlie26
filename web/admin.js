// Console di regia: configurazione dell'evento (modalità, canali, utenti).
(() => {
  'use strict';

  const $ = (id) => document.getElementById(id);
  const root = $('config');
  let ev = null;
  let tab = 'evento';
  try { tab = localStorage.getItem('regia.tab') || 'evento'; } catch (_) {}

  const micModes = [['libero', 'Libero'], ['ptt', 'Solo push-to-talk'], ['admin', 'Solo il caporeparto apre']];

  function el(tag, attrs, ...children) {
    const e = document.createElement(tag);
    for (const [k, v] of Object.entries(attrs || {})) {
      if (k === 'class') e.className = v;
      else if (k.startsWith('on')) e.addEventListener(k.slice(2), v);
      else if (k === 'checked' || k === 'value' || k === 'disabled') e[k] = v;
      else e.setAttribute(k, v);
    }
    for (const c of children) if (c != null) e.append(c);
    return e;
  }

  function notify(text, isError) {
    const b = $('configMsg');
    b.textContent = text;
    b.className = 'banner' + (isError ? ' error' : '');
    clearTimeout(notify.t);
    notify.t = setTimeout(() => b.classList.add('hidden'), isError ? 8000 : 2500);
  }

  async function load() {
    const r = await fetch('/api/regia/evento');
    if (!r.ok) {
      root.textContent = 'La configurazione è disponibile solo sul computer di regia.';
      return;
    }
    ev = await r.json();
    render();
  }

  async function op(body, okText) {
    const r = await fetch('/api/regia/evento', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body) });
    const out = await r.json();
    if (!r.ok) {
      notify(out.error || 'Operazione non riuscita', true);
      return false;
    }
    ev = out;
    render();
    if (okText) notify(okText);
    return true;
  }

  function renderTabs() {
    const nav = $('configTabs');
    nav.textContent = '';
    const cred = ev.mode === 'credenziali';
    for (const [id, label] of [['evento', 'Evento'], ['canali', 'Canali (' + ev.channels.length + ')'], ['utenti', cred ? 'Utenti (' + (ev.users || []).length + ')' : 'Utenti'], ['video', 'Video'], ['registro', 'Registro']]) {
      nav.append(el('button', { class: tab === id ? 'sel' : '', onclick: () => { tab = id; try { localStorage.setItem('regia.tab', id); } catch (_) {} render(); } }, label));
    }
  }

  function render() {
    renderTabs();
    const body = $('configBody');
    body.textContent = '';
    clearInterval(logTimer);
    ({ evento: renderEvent, canali: renderChannels, utenti: renderUsers, video: renderVideo, registro: renderLog }[tab] || renderEvent)(body);
  }

  function renderEvent(body) {
    // Evento e modalità
    const nameIn = el('input', { type: 'text', value: ev.name, maxlength: '40' });
    body.append(el('div', { class: 'row' },
      el('label', { class: 'muted' }, 'Nome evento'), nameIn,
      el('button', { class: 'mini primary', onclick: () => op({ op: 'name', name: nameIn.value }, 'Nome salvato') }, 'Salva')));

    const modes = el('div', { class: 'modes' });
    for (const [mode, title, desc] of [
      ['libero', 'Libero accesso', 'QR unico. Chi entra scrive il nome e sceglie il canale. Chiunque può creare un sottocanale (anche con password) e ne diventa caporeparto.'],
      ['credenziali', 'Accesso con credenziali', 'Ognuno entra con utente e password creati qui sotto. Ruoli e canali li decide la regia.'],
    ]) {
      const radio = el('input', { type: 'radio', name: 'mode', checked: ev.mode === mode });
      radio.addEventListener('change', async () => {
        const msg = mode === 'credenziali'
          ? 'Passare all\'accesso con credenziali? Chi è collegato in libero accesso verrà scollegato.'
          : 'Passare al libero accesso? Chi è collegato con le credenziali verrà scollegato.';
        if (!confirm(msg)) { render(); return; }
        await op({ op: 'mode', mode }, 'Modalità cambiata');
      });
      modes.append(el('label', { class: ev.mode === mode ? 'sel' : '' }, radio, el('b', {}, title), el('div', { class: 'small muted' }, desc)));
    }
    body.append(modes);

    // Import / export della configurazione
    body.append(el('h2', {}, 'Salva o carica la configurazione'));
    const file = el('input', { type: 'file', accept: '.json,application/json', class: 'hidden' });
    file.addEventListener('change', async () => {
      const f = file.files[0];
      file.value = '';
      if (!f) return;
      if (!confirm('Caricare "' + f.name + '"? Canali, utenti e modalità attuali verranno sostituiti; chi è collegato viene aggiornato o scollegato.')) return;
      const r = await fetch('/api/regia/importa', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: await f.text() });
      const out = await r.json();
      if (!r.ok) { notify(out.error || 'Importazione non riuscita', true); return; }
      ev = out;
      render();
      notify('Configurazione caricata da ' + f.name);
    });
    body.append(el('div', { class: 'row' },
      el('a', { class: 'mini primary', href: '/api/regia/esporta', download: '' }, '⬇ Esporta configurazione'),
      el('button', { class: 'mini', onclick: () => file.click() }, '⬆ Importa configurazione…'),
      file));
    body.append(el('p', { class: 'small muted' }, 'Il file contiene canali, utenti e impostazioni (le password solo come impronta non leggibile). ' +
      'Usalo per preparare un evento in anticipo o riutilizzarlo come modello.'));

    body.append(el('h2', {}, 'Programma'));
    body.append(el('div', { class: 'row' }, el('button', {
      class: 'mini danger',
      onclick: async () => {
        if (!confirm('Spegnere l\'intercom? Tutti i tecnici verranno scollegati.')) return;
        await fetch('/api/regia/spegni', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: '{}' });
        document.body.innerHTML = '<div class="doc"><h1>Intercom spento</h1><p class="muted">Per riaccenderlo avvia di nuovo il programma.</p></div>';
      },
    }, '⏻ Spegni il programma'), el('span', { class: 'small muted' }, 'Su Windows si può spegnere anche dall\'icona vicino all\'orologio.')));
  }

  // Registro: i messaggi che prima comparivano nella finestra nera.
  let logTimer = null;
  function renderLog(body) {
    const pre = el('pre', { class: 'log' });
    body.append(el('p', { class: 'small muted' }, 'Messaggi del programma (gli stessi salvati nel file intercom.log della cartella dati).'), pre);
    const refresh = async () => {
      try {
        const r = await fetch('/api/regia/registro');
        const out = await r.json();
        const atBottom = pre.scrollTop + pre.clientHeight >= pre.scrollHeight - 20;
        pre.textContent = (out.lines || []).join('\n');
        if (atBottom) pre.scrollTop = pre.scrollHeight;
      } catch (_) { /* server spento */ }
    };
    refresh();
    logTimer = setInterval(refresh, 2000);
  }

  function renderVideo(body) {
    body.append(el('p', { class: 'muted' }, 'L\'invio del video della regia (da OBS via WHIP o da una sorgente del browser) arriva nella Fase 5. ' +
      'Qui sceglierai le sorgenti e la qualità; le anteprime compariranno nella colonna di sinistra (massimo 15 fps).'));
    body.append(el('p', { class: 'muted' }, 'Già da ora puoi decidere in quali canali arriverà il video: colonna "Video" nella scheda Canali.'));
  }

  function renderChannels(body) {
    const cred = ev.mode === 'credenziali';
    const ct = el('table', {}, el('tr', {},
      el('th', {}, 'Nome'), el('th', {}, 'Microfono utenti'), el('th', {}, 'Video'),
      cred ? null : el('th', {}, 'Password'), el('th', {}, '')));
    for (const c of ev.channels) {
      const name = el('input', { type: 'text', value: c.name, maxlength: '24' });
      const rename = () => { if (name.value.trim() && name.value.trim() !== c.name) op({ op: 'renameChannel', id: c.id, name: name.value }, 'Canale rinominato'); };
      name.addEventListener('change', rename);
      const mic = el('select', {}, ...micModes.map(([v, l]) => el('option', { value: v }, l)));
      mic.value = c.micMode;
      const video = el('input', { type: 'checkbox', checked: !!c.video });
      const saveSettings = () => op({ op: 'channelSettings', id: c.id, micMode: mic.value, video: video.checked }, 'Impostazioni salvate');
      mic.addEventListener('change', saveSettings);
      video.addEventListener('change', saveSettings);
      let pwd = null;
      if (!cred) {
        if (c.main) pwd = el('td', { class: 'muted small' }, 'aperto a tutti');
        else {
          const pin = el('input', { type: 'password', placeholder: c.passwordHash ? '•••• (cambia)' : 'nessuna', autocomplete: 'new-password' });
          pwd = el('td', {}, el('div', { class: 'row' }, pin,
            el('button', { class: 'mini', onclick: () => op({ op: 'channelPassword', id: c.id, password: pin.value }, pin.value ? 'Password impostata' : 'Password rimossa') }, pin.value || !c.passwordHash ? 'Imposta' : 'Togli')));
          pin.addEventListener('input', () => { pwd.querySelector('button').textContent = pin.value || !c.passwordHash ? 'Imposta' : 'Togli'; });
        }
      }
      const del = c.main ? el('span', { class: 'small muted' }, 'principale')
        : el('button', { class: 'mini danger', onclick: () => { if (confirm('Eliminare il canale ' + c.name + '? Chi ne fa parte ne verrà tolto.')) op({ op: 'removeChannel', id: c.id }, 'Canale eliminato'); } }, 'Elimina');
      ct.append(el('tr', {}, el('td', {}, name), el('td', {}, mic), el('td', {}, video), pwd, el('td', {}, del)));
    }
    body.append(el('div', { class: 'scroll' }, ct));
    const newCh = el('input', { type: 'text', placeholder: 'Nuovo canale (es. Palco)', maxlength: '24' });
    const addCh = () => op({ op: 'addChannel', name: newCh.value }, 'Canale aggiunto');
    newCh.addEventListener('keydown', (e) => { if (e.key === 'Enter') addCh(); });
    body.append(el('div', { class: 'row' }, newCh, el('button', { class: 'mini primary', onclick: addCh }, 'Aggiungi canale')));

  }

  function renderUsers(body) {
    const cred = ev.mode === 'credenziali';
    if (!cred) {
      body.append(el('p', { class: 'small muted' }, 'Gli utenti con password servono solo nell\'accesso con credenziali.'));
      return;
    }
    body.append(el('h2', {}, 'Utenti'));
    const ut = el('table', {}, el('tr', {},
      el('th', {}, 'Utente'), el('th', {}, 'Nome mostrato'), el('th', {}, 'Ruolo'), el('th', {}, 'Canali'), el('th', {}, 'Nuova password'), el('th', {}, '')));
    const userRow = (u, isNew) => {
      const username = el('input', { type: 'text', value: u.username || '', placeholder: 'es. cam1', autocapitalize: 'off' });
      if (!isNew) username.disabled = true;
      const name = el('input', { type: 'text', value: u.name || '', placeholder: 'es. Marco – Cam 1' });
      const role = el('select', {}, el('option', { value: 'user' }, 'Tecnico'), el('option', { value: 'admin' }, 'Caporeparto'));
      role.value = u.role || 'user';
      const chips = el('div', { class: 'chips' });
      for (const c of ev.channels) {
        const box = el('input', { type: 'checkbox', value: c.id, checked: (u.channels || []).includes(c.id) });
        chips.append(el('label', {}, box, c.name));
      }
      const pass = el('input', { type: 'password', placeholder: isNew ? 'obbligatoria' : 'lascia vuoto', autocomplete: 'new-password' });
      const save = () => op({
        op: 'setUser',
        user: { username: username.value.trim(), name: name.value, role: role.value, channels: [...chips.querySelectorAll('input:checked')].map((b) => b.value) },
        password: pass.value,
      }, isNew ? 'Utente creato' : 'Utente salvato');
      const actions = el('td', {}, el('button', { class: 'mini primary', onclick: save }, isNew ? 'Crea' : 'Salva'),
        isNew ? null : el('button', { class: 'mini danger', onclick: () => { if (confirm('Eliminare l\'utente ' + u.username + '?')) op({ op: 'removeUser', user: { username: u.username } }, 'Utente eliminato'); } }, 'Elimina'));
      return el('tr', {}, el('td', {}, username), el('td', {}, name), el('td', {}, role), el('td', {}, chips), el('td', {}, pass), actions);
    };
    for (const u of ev.users || []) ut.append(userRow(u, false));
    ut.append(userRow({ role: 'user', channels: [] }, true));
    body.append(el('div', { class: 'scroll' }, ut));
    body.append(el('p', { class: 'small muted' }, 'Il caporeparto parla sempre anche nel canale principale. Le modifiche valgono subito anche per chi è già collegato.'));
  }

  // ---------- Scheda di rete usata nei QR ----------
  const netSel = $('netSelect');
  async function loadNet(info) {
    if (!netSel) return;
    if (!info) {
      const r = await fetch('/api/regia/rete');
      if (!r.ok) return;
      info = await r.json();
    }
    netSel.textContent = '';
    let found = false;
    for (const a of info.ips || []) {
      const sel = info.host === a.ip || info.host.startsWith(a.ip.replace(/\./g, '-') + '.');
      found = found || sel;
      netSel.append(el('option', { value: a.ip }, a.ip + ' — ' + a.interface));
      if (sel) netSel.value = a.ip;
    }
    if (!found) {
      netSel.prepend(el('option', { value: '' }, info.host + ' (non più attiva)'));
      netSel.value = '';
    }
    netSel.disabled = !info.canChange;
    // Aggiorna QR e indirizzi mostrati.
    const bust = '?t=' + Date.now();
    $('qrApp').src = '/qr/app.png' + bust;
    $('qrHelp').src = '/qr/aiuto.png' + bust;
    $('appUrl').textContent = info.appURL;
  }
  if (netSel) {
    netSel.addEventListener('change', async () => {
      if (!netSel.value) return;
      const r = await fetch('/api/regia/rete', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ ip: netSel.value }) });
      const out = await r.json();
      if (!r.ok) { notify(out.error || 'Cambio rete non riuscito', true); return; }
      loadNet(out);
      notify('I QR ora puntano a ' + out.host + '. Chi è già collegato non viene disturbato.');
    });
    $('netRefresh').addEventListener('click', () => loadNet());
    loadNet();
  }

  load();
})();
