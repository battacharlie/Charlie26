// Console di regia: configurazione dell'evento (modalità, canali, utenti).
(() => {
  'use strict';

  const $ = (id) => document.getElementById(id);
  const root = $('config');
  let ev = null;

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

  function render() {
    const body = $('configBody');
    body.textContent = '';
    const cred = ev.mode === 'credenziali';

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

    // Canali
    body.append(el('h2', {}, 'Canali'));
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

    // Utenti
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

  load();
})();
