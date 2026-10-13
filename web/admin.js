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

  let isRemote = false; // questo è un telefono abbinato (non il PC di regia)

  async function load() {
    if (window.regiaPairing) return; // abbinamento in corso: la pagina si ricarica
    const r = await fetch('/api/regia/evento');
    if (!r.ok) {
      notPaired();
      return;
    }
    ev = await r.json();
    render();
    loadDevices();
  }

  // Telefono non abbinato: spiega come fare invece di mostrare una console vuota.
  function notPaired() {
    document.body.classList.add('unpaired');
    document.body.innerHTML = '';
    document.body.append(el('div', { class: 'doc' },
      el('div', { class: 'brand big' }, el('img', { src: '/static/brand/icon-192.png', alt: '' }),
        el('div', {}, el('span', { class: 'word' }, 'CHARLIE', el('b', {}, '26')), el('span', { class: 'tag' }, 'INTERCOM EVOLUTO'))),
      el('h1', {}, 'Regia da smartphone'),
      el('p', {}, 'Questo telefono non è ancora abbinato come regia.'),
      el('ol', {},
        el('li', {}, 'Sul computer di regia, nella console, premi ', el('b', {}, '📱 Abbina telefono'), '.'),
        el('li', {}, 'Inquadra il QR che compare con la fotocamera (o con l\'app CHARLIE26).'),
        el('li', {}, 'Il telefono diventa una seconda regia, con gli stessi comandi del computer.')),
      el('p', { class: 'muted small' }, 'Il codice vale 5 minuti e una sola volta. Dalla console puoi revocare l\'abbinamento quando vuoi.'),
      el('p', {}, el('a', { class: 'btn', href: '/' }, 'Entra invece come tecnico'))));
  }

  // ---------- Telefoni abbinati come regia ----------
  let devices = [];
  async function loadDevices() {
    try {
      const r = await fetch('/api/regia/dispositivi');
      if (!r.ok) return;
      devices = await r.json();
      isRemote = devices.some((d) => d.io);
      document.body.classList.toggle('remote', isRemote);
      buildMobileBar();
      if (tab === 'evento') render();
    } catch (_) {}
  }

  async function revokeDevice(d) {
    const msg = d.io ? 'Scollegare questo telefono dalla regia? Per tornare regia servirà un nuovo QR dalla console.' : 'Revocare "' + d.nome + '"? Il telefono perde subito i comandi di regia.';
    if (!confirm(msg)) return;
    const r = await fetch('/api/regia/dispositivi', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ op: 'revoca', id: d.id }) });
    if (d.io) { location.reload(); return; }
    if (!r.ok) { notify('Revoca non riuscita', true); return; }
    notify('"' + d.nome + '" non è più regia');
    loadDevices();
  }

  // Finestra con il QR di abbinamento: si chiude da sola quando il telefono è abbinato.
  async function pairDialog() {
    const r = await fetch('/api/regia/abbina', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: '{}' });
    const out = await r.json();
    if (!r.ok) { notify(out.error || 'Abbinamento non disponibile', true); return; }
    const before = new Set(devices.map((d) => d.id));
    const left = el('span', {});
    const dlg = el('dialog', { class: 'pair-dialog' },
      el('h2', {}, '📱 Abbina un telefono come regia'),
      el('p', { class: 'small' }, 'Inquadra il QR con la fotocamera del telefono (o con l\'app CHARLIE26). Il telefono avrà gli stessi comandi della regia; questa console resta attiva.'),
      el('img', { class: 'qr', src: '/qr/abbina.png?codice=' + out.codice, alt: 'QR di abbinamento' }),
      el('p', { class: 'small muted' }, 'Vale una sola volta · scade tra ', left),
      el('div', { class: 'row' }, el('button', { class: 'mini', onclick: () => dlg.close() }, 'Chiudi')));
    document.body.append(dlg);
    dlg.showModal();
    const exp = new Date(out.scade).getTime();
    const tick = setInterval(async () => {
      const s = Math.max(0, Math.round((exp - Date.now()) / 1000));
      left.textContent = Math.floor(s / 60) + ':' + String(s % 60).padStart(2, '0');
      if (s === 0) { dlg.close(); notify('Codice di abbinamento scaduto', true); }
      if (s % 2 === 0) {
        await loadDevices();
        const nuovo = devices.find((d) => !before.has(d.id));
        if (nuovo && dlg.open) { dlg.close(); notify('"' + nuovo.nome + '" ora è regia'); }
      }
    }, 1000);
    left.textContent = '5:00';
    dlg.addEventListener('close', () => { clearInterval(tick); dlg.remove(); });
  }

  function renderDevices(body) {
    body.append(el('h2', {}, 'Regia da smartphone'));
    body.append(el('p', { class: 'small muted' }, 'Ogni telefono abbinato ha gli stessi comandi della regia (anche più di uno insieme). Questa console resta sempre attiva.'));
    const list = el('div', { class: 'devices' });
    for (const d of devices) {
      const seen = new Date(d.visto);
      list.append(el('div', { class: 'row device' },
        el('span', {}, '📱 ', el('b', {}, d.nome), d.io ? el('span', { class: 'tag-me' }, 'questo telefono') : null),
        el('span', { class: 'small muted' }, 'visto ' + seen.toLocaleDateString('it-IT') + ' ' + seen.toLocaleTimeString('it-IT', { hour: '2-digit', minute: '2-digit' })),
        el('button', { class: 'mini danger', onclick: () => revokeDevice(d) }, d.io ? 'Scollega' : 'Revoca')));
    }
    if (!devices.length) list.append(el('p', { class: 'small muted' }, 'Nessun telefono abbinato.'));
    body.append(list, el('div', { class: 'row' }, el('button', { class: 'mini primary', onclick: pairDialog }, '📱 Abbina telefono')));
  }

  async function postLook(change, okText) {
    const r = await fetch('/api/regia/aspetto', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(change) });
    const out = await r.json();
    if (!r.ok) { notify(out.error || 'Salvataggio non riuscito', true); return; }
    ev = out;
    render();
    notify(okText);
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

  // Importa / esporta: sempre a portata nella riga delle schede.
  let importFile = null;
  function setupFileActions() {
    const file = el('input', { type: 'file', accept: '.json,application/json', class: 'hidden' });
    importFile = file;
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
    const tip = 'Il file contiene canali, utenti e impostazioni (le password solo come impronta non leggibile). Usalo per preparare un evento in anticipo o riutilizzarlo come modello.';
    $('configActions').append(
      el('button', { class: 'mini', onclick: pairDialog, title: 'Abbina uno smartphone come seconda regia' }, '📱 Abbina telefono'),
      el('a', { class: 'mini primary', href: '/api/regia/esporta', download: '', title: tip }, '⬇ Esporta'),
      el('button', { class: 'mini', onclick: () => file.click(), title: tip }, '⬆ Importa…'),
      file,
      el('button', { class: 'mini danger', onclick: shutdown, title: 'Scollega tutti i tecnici e chiude il programma (su Windows anche dall\'icona vicino all\'orologio)' }, '⏻ Spegni'));
  }

  // Spegne il programma e chiude solo questa scheda del browser.
  async function shutdown() {
    if (!confirm('Terminare l\'evento e spegnere CHARLIE26? Tutti i tecnici (e gli altri telefoni di regia) verranno scollegati.')) return;
    try {
      await fetch('/api/regia/spegni', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: '{}' });
    } catch (_) {}
    document.body.innerHTML = '<div class="doc"><h1>CHARLIE26 spento</h1><p class="muted">Puoi chiudere questa scheda. Per riaccenderlo avvia di nuovo il programma.</p></div>';
    if (window.Capacitor && window.Capacitor.Plugins && window.Capacitor.Plugins.App) { window.Capacitor.Plugins.App.exitApp(); return; }
    // I browser permettono a una pagina di chiudere la propria scheda solo se è stata
    // aperta dal programma (o da uno script); altrimenti resta il messaggio qui sopra.
    window.close();
  }

  function tabList() {
    const cred = ev && ev.mode === 'credenziali';
    return [['evento', 'Evento'], ['canali', 'Canali' + (ev ? ' (' + ev.channels.length + ')' : '')], ['utenti', cred ? 'Utenti (' + (ev.users || []).length + ')' : 'Utenti'], ['qr', 'QR'], ['video', 'Video'], ['registro', 'Registro']];
  }

  function selectTab(id) {
    tab = id;
    try { localStorage.setItem('regia.tab', id); } catch (_) {}
    if (ev) render();
  }

  function renderTabs() {
    const nav = $('configTabs');
    nav.textContent = '';
    for (const [id, label] of tabList()) {
      nav.append(el('button', { class: tab === id ? 'sel' : '', onclick: () => selectTab(id) }, label));
    }
  }

  // ---------- Smartphone: schermate come pulsanti in alto + menu ----------
  const mobileQuery = window.matchMedia('(max-width: 760px), (max-height: 520px) and (pointer: coarse)');
  let mview = 'intercom';
  try { mview = localStorage.getItem('regia.mview') || 'intercom'; } catch (_) {}

  function setMobileView(v) {
    mview = v;
    try { localStorage.setItem('regia.mview', v); } catch (_) {}
    document.body.dataset.mview = v;
    if (v !== 'intercom' && tab !== v) selectTab(v);
    buildMobileBar();
    window.scrollTo(0, 0);
  }

  function buildMobileBar() {
    const bar = $('mbar');
    if (!bar) return;
    document.body.classList.toggle('mobile', mobileQuery.matches);
    document.body.dataset.mview = mview;
    bar.textContent = '';
    const row = el('div', { class: 'mbar-tabs' });
    for (const [id, label] of [['intercom', '🎙 Intercom'], ...tabList()]) {
      row.append(el('button', { class: mview === id ? 'sel' : '', onclick: () => setMobileView(id) }, label));
    }
    const menuBtn = el('button', { class: 'mbar-menu', 'aria-label': 'Menu', title: 'Menu' }, '☰');
    menuBtn.addEventListener('click', (e) => { e.stopPropagation(); toggleMenu(); });
    bar.append(row, menuBtn);
    const sel = row.querySelector('.sel');
    if (sel) sel.scrollIntoView({ block: 'nearest', inline: 'center' });
  }

  function toggleMenu() {
    const old = document.querySelector('.mmenu');
    if (old) { old.remove(); return; }
    const item = (label, fn, cls) => el('button', { class: cls || '', onclick: () => { menu.remove(); fn(); } }, label);
    const menu = el('div', { class: 'mmenu', role: 'menu' },
      item('⬇ Esporta configurazione', exportConfig),
      item('⬆ Importa configurazione…', () => importFile.click()),
      item('📱 Abbina un altro telefono', pairDialog),
      el('hr'),
      item('⏹ Termina evento (spegne CHARLIE26)', shutdown, 'danger'),
      item(window.Capacitor ? '✕ Chiudi app' : '✕ Esci dalla regia', closeApp));
    document.body.append(menu);
    setTimeout(() => document.addEventListener('click', (e) => { if (!menu.contains(e.target)) menu.remove(); }, { once: true }), 0);
  }

  // Esporta: link diretto sul PC; sul telefono (e nell'app) passa da un file scaricato.
  async function exportConfig() {
    const r = await fetch('/api/regia/esporta');
    if (!r.ok) { notify('Esportazione non riuscita', true); return; }
    const blob = await r.blob();
    const name = (/filename="?([^";]+)/.exec(r.headers.get('Content-Disposition') || '') || [])[1] || 'evento.json';
    if (window.charlieApp && window.charlieApp.saveFile) {
      window.charlieApp.saveFile(name, await blob.text());
      return;
    }
    const a = el('a', { href: URL.createObjectURL(blob), download: name });
    document.body.append(a);
    a.click();
    setTimeout(() => { URL.revokeObjectURL(a.href); a.remove(); }, 1000);
  }

  // Chiudi: lascia il pannello senza spegnere niente per gli altri.
  function closeApp() {
    if (window.intercomLeave) window.intercomLeave();
    if (window.Capacitor && window.Capacitor.Plugins && window.Capacitor.Plugins.App) {
      window.Capacitor.Plugins.App.exitApp();
      return;
    }
    document.body.innerHTML = '<div class="doc"><h1>Uscito dalla regia</h1><p class="muted">CHARLIE26 resta acceso per tutti. Puoi chiudere questa scheda.</p><p><a class="btn" href="/regia">Rientra</a></p></div>';
    window.close();
  }

  mobileQuery.addEventListener('change', buildMobileBar);

  function render() {
    renderTabs();
    buildMobileBar();
    const body = $('configBody');
    body.textContent = '';
    clearInterval(logTimer);
    ({ evento: renderEvent, canali: renderChannels, utenti: renderUsers, video: renderVideo, registro: renderLog, qr: renderQR }[tab] || renderEvent)(body);
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

    // Aspetto delle pagine dei tecnici
    body.append(el('h2', {}, 'Aspetto delle pagine dei tecnici'));
    const sw = el('span', { class: 'swatch' });
    sw.style.background = ev.look && ev.look.image
      ? 'url(/img/' + ev.look.image + ') center / cover, ' + (ev.look.color || '#0d0f12')
      : ((ev.look && ev.look.color) || '#0d0f12');
    body.append(el('div', { class: 'row' }, sw,
      el('button', { class: 'mini primary', onclick: () => window.intercomLook.open({
        title: 'Sfondo delle pagine dei tecnici', withIcon: false, look: ev.look || {},
        onSave: (ch) => postLook(Object.assign({ id: '' }, ch), 'Sfondo salvato'),
      }) }, '🎨 Colore o immagine di sfondo'),
      el('span', { class: 'small muted' }, 'Vale per la pagina di accesso e per chi non ha uno sfondo del proprio canale.')));

    // Modelli salvati sul computer di regia
    renderDevices(body);

    body.append(el('h2', {}, 'Modelli di evento'));
    const modelName = el('input', { type: 'text', placeholder: 'Nome del modello (es. Concerto piazza)', maxlength: '60', value: ev.name });
    const models = el('div', { class: 'models' });
    const callModels = async (payload, okText) => {
      const r = await fetch('/api/regia/modelli', payload ? { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(payload) } : {});
      const out = await r.json();
      if (!r.ok) { notify(out.error || 'Operazione non riuscita', true); return; }
      paintModels(out);
      if (okText) notify(okText);
      if (payload && payload.op === 'load') load();
    };
    const paintModels = (list) => {
      models.textContent = '';
      if (!list.length) models.append(el('p', { class: 'small muted' }, 'Nessun modello salvato.'));
      for (const m of list) {
        const when = m.salvato ? new Date(m.salvato).toLocaleString('it-IT', { dateStyle: 'short', timeStyle: 'short' }) : '';
        models.append(el('div', { class: 'model' },
          el('div', { class: 'who' }, el('b', {}, m.nome),
            el('span', { class: 'small muted' }, (m.mode === 'credenziali' ? 'credenziali' : 'libero accesso') + ' · ' + m.canali + ' canali' + (m.utenti ? ' · ' + m.utenti + ' utenti' : '') + (when ? ' · ' + when : ''))),
          el('button', { class: 'mini primary', onclick: () => { if (confirm('Caricare il modello "' + m.nome + '"? La configurazione attuale verrà sostituita.')) callModels({ op: 'load', file: m.file }, 'Modello "' + m.nome + '" caricato'); } }, 'Carica'),
          el('button', { class: 'mini danger', onclick: () => { if (confirm('Eliminare il modello "' + m.nome + '"?')) callModels({ op: 'delete', file: m.file }, 'Modello eliminato'); } }, 'Elimina')));
      }
    };
    body.append(el('div', { class: 'row' }, modelName,
      el('button', { class: 'mini primary', onclick: () => callModels({ op: 'save', nome: modelName.value }, 'Modello salvato') }, '💾 Salva come modello')), models);
    callModels();

  }

  // QR: generale, per canale (libero accesso) o per utente (credenziali).
  function qrCard(src, title, sub, file, extra) {
    return el('div', { class: 'qr-card' },
      el('img', { src: src + (src.includes('?') ? '&' : '?') + 't=' + Date.now(), alt: '' }),
      el('div', { class: 'qr-card-text' }, el('b', {}, title), el('span', { class: 'small muted' }, sub),
        el('div', { class: 'row' }, el('a', { class: 'mini', href: src, download: file }, 'PNG'), extra)));
  }
  function renderQR(body) {
    const cred = ev.mode === 'credenziali';
    body.append(el('div', { class: 'row' },
      el('button', { class: 'mini primary', onclick: () => window.open('/regia/qr', '_blank') }, '🖨 Stampa / salva PDF'),
      el('span', { class: 'small muted' }, cred
        ? 'Il QR personale fa entrare direttamente, senza password: consegnalo solo alla persona giusta.'
        : 'Il QR di un canale apre l\'app con quel canale già scelto.')));
    const grid = el('div', { class: 'qr-grid' });
    grid.append(qrCard('/qr/app.png', 'Tutti', cred ? 'pagina di accesso' : 'scelgono il canale', 'intercom-tutti.png'));
    if (cred) {
      for (const u of ev.users || []) {
        const renew = el('button', { class: 'mini', title: 'Il QR vecchio smette di funzionare', onclick: () => {
          if (confirm('Creare un nuovo QR per ' + u.name + '? Quello vecchio non funzionerà più.')) op({ op: 'newQRKey', user: { username: u.username } }, 'Nuovo QR creato');
        } }, 'Nuovo codice');
        const chs = (u.channels || []).map((id) => (ev.channels.find((c) => c.id === id) || {}).name).filter(Boolean).join(', ');
        grid.append(qrCard('/qr/utente/' + encodeURIComponent(u.username) + '.png', u.name, (u.role === 'admin' ? 'Caporeparto' : 'Tecnico') + (chs ? ' · ' + chs : ''), 'intercom-' + u.username + '.png', renew));
      }
      if (!(ev.users || []).length) grid.append(el('p', { class: 'muted' }, 'Crea gli utenti nella scheda Utenti: qui compariranno i loro QR personali.'));
    } else {
      for (const c of ev.channels) grid.append(qrCard('/qr/app.png?canale=' + encodeURIComponent(c.id), c.name, c.main ? 'canale principale' : 'reparto', 'intercom-' + c.id + '.png'));
    }
    body.append(grid);
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

  // Video: sorgenti del browser della regia (schede di acquisizione,
  // OBS Virtual Camera, vMix Video…) e richieste da approvare.
  let videoDevices = [];
  function renderVideo(body) {
    const iv = window.intercomVideo;
    if (!iv || !iv.joined()) {
      body.append(el('p', { class: 'muted' }, 'Per inviare video apri prima il pannello intercom qui sotto ("Apri il pannello").'));
      iv && (iv.onchange = () => { if (tab === 'video') render(); });
      return;
    }
    iv.onchange = () => { if (tab === 'video') render(); };

    body.append(el('h2', {}, 'Invia un video dal computer di regia'));
    const devSel = el('select', {});
    const fill = () => {
      devSel.textContent = '';
      if (!videoDevices.length) devSel.append(el('option', { value: '' }, 'Premi "Cerca periferiche"'));
      for (const d of videoDevices) devSel.append(el('option', { value: d.deviceId }, d.label || 'Periferica video'));
    };
    fill();
    const search = async () => {
      try {
        // Serve un primo permesso per conoscere i nomi delle periferiche.
        const tmp = await navigator.mediaDevices.getUserMedia({ video: true });
        tmp.getTracks().forEach((t) => t.stop());
      } catch (e) { notify('Accesso alle periferiche video negato (' + e.name + ')', true); }
      videoDevices = (await navigator.mediaDevices.enumerateDevices()).filter((d) => d.kind === 'videoinput');
      fill();
      notify(videoDevices.length + ' periferiche video trovate');
    };
    const label = el('input', { type: 'text', placeholder: 'Titolo (es. Programma, Camera 2)', maxlength: '32', value: 'Programma' });
    const chips = el('div', { class: 'chips' });
    for (const c of iv.myChannels()) chips.append(el('label', {}, el('input', { type: 'checkbox', value: c, checked: true }), iv.channelName(c)));
    const start = async () => {
      const chs = [...chips.querySelectorAll('input:checked')].map((x) => x.value);
      if (!devSel.value) { notify('Scegli una periferica', true); return; }
      if (!chs.length) { notify('Scegli almeno un canale', true); return; }
      try {
        await iv.start({ deviceId: devSel.value, label: label.value.trim() || 'Video regia', channels: chs });
        notify('Video avviato');
      } catch (e) { notify('Impossibile avviare la periferica (' + e.name + ')', true); }
    };
    body.append(el('div', { class: 'row' }, devSel, el('button', { class: 'mini', onclick: search }, '🔍 Cerca periferiche')));
    body.append(el('div', { class: 'row' }, label, el('button', { class: 'mini primary', onclick: start }, '▶ Avvia')));
    body.append(el('div', { class: 'row' }, el('span', { class: 'small muted' }, 'Canali:'), chips));
    body.append(el('p', { class: 'small muted' }, 'OBS: usa "Avvia fotocamera virtuale" e scegli OBS Virtual Camera. vMix: attiva l\'uscita "Virtual Camera" (vMix Video). ' +
      'Il video arriva in un canale solo se il suo caporeparto ha attivato "Video della regia" (scheda Canali o ⚙).'));

    const mine = iv.locals();
    if (mine.length) {
      body.append(el('h2', {}, 'Stai inviando'));
      for (const l of mine) {
        body.append(el('div', { class: 'model' },
          el('div', { class: 'who' }, el('b', {}, l.label), el('span', { class: 'small muted' }, l.channels.map(iv.channelName).join(', '))),
          el('button', { class: 'mini danger', onclick: () => iv.stop(l.ref) }, 'Ferma')));
      }
    }

    // Ingresso da OBS (WHIP)
    body.append(el('h2', {}, 'Video da OBS (WHIP)'));
    const whipBox = el('div', {});
    body.append(whipBox);
    const paintWhip = (w) => {
      whipBox.textContent = '';
      const copy = (txt) => el('button', { class: 'mini', onclick: () => { navigator.clipboard.writeText(txt).then(() => notify('Copiato')).catch(() => notify('Copia non riuscita: selezionalo a mano', true)); } }, 'Copia');
      const line = (label, txt) => el('div', { class: 'row' }, el('span', { class: 'small muted', style: 'min-width:9rem' }, label), el('code', { class: 'url' }, txt), copy(txt));
      whipBox.append(
        el('p', { class: 'small muted' }, 'In OBS: Impostazioni → Trasmissione → Servizio "WHIP". Server: uno degli indirizzi qui sotto (aggiungi ?titolo=Programma per dargli un nome). Bearer Token: la chiave.'),
        line('Server (consigliato)', w.urlHTTP + '?titolo=Programma'),
        line('Server (https)', w.urlHTTPS + '?titolo=Programma'),
        line('Bearer Token', w.token),
        el('div', { class: 'row' }, el('button', { class: 'mini', onclick: async () => {
          if (!confirm('Creare una nuova chiave? OBS dovrà usare quella nuova.')) return;
          const r = await fetch('/api/regia/whip', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: '{"renew":true}' });
          paintWhip(await r.json());
        } }, 'Nuova chiave'), el('span', { class: 'small muted' }, 'L\'indirizzo https funziona solo se il computer con OBS ha installato il certificato della regia.')));
    };
    fetch('/api/regia/whip').then((r) => r.json()).then(paintWhip).catch(() => {});

    const others = iv.list().filter((v) => !mine.some((l) => l.id === v.id));
    body.append(el('h2', {}, 'Video dei partecipanti'));
    if (!others.length) body.append(el('p', { class: 'small muted' }, 'Nessuno. Quando un tecnico chiede di mostrare un video compare qui (e nelle anteprime) per l\'approvazione.'));
    for (const v of others) {
      body.append(el('div', { class: 'model' },
        el('div', { class: 'who' }, el('b', {}, v.label + (v.approved ? '' : ' — da approvare')),
          v.owner ? el('span', { class: 'small muted' }, (v.ownerName || '') + ' · ' + (v.channels || []).map(iv.channelName).join(', '))
            : (() => {
              // Video di OBS: la regia sceglie i canali.
              const box = el('div', { class: 'chips' });
              for (const c of iv.myChannels()) {
                const cb = el('input', { type: 'checkbox', value: c, checked: (v.channels || []).includes(c) });
                cb.addEventListener('change', () => iv.setChannels(v.id, [...box.querySelectorAll('input:checked')].map((x) => x.value)));
                box.append(el('label', {}, cb, iv.channelName(c)));
              }
              return box;
            })()),
        v.approved ? null : el('button', { class: 'mini primary', onclick: () => iv.approve(v.id, true) }, 'Approva'),
        el('button', { class: 'mini danger', onclick: () => (v.approved ? iv.stopRemote(v.id) : iv.approve(v.id, false)) }, v.approved ? 'Ferma' : 'Rifiuta')));
    }
  }

  function renderChannels(body) {
    const cred = ev.mode === 'credenziali';
    const ct = el('table', {}, el('tr', {},
      el('th', {}, ''), el('th', {}, 'Nome'), el('th', {}, 'Microfono utenti'), el('th', {}, 'Video'),
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
      const icon = c.icon ? el('img', { class: 'chan-icon', alt: '', src: /^m[1-4]$/.test(c.icon) ? '/static/avatars/' + c.icon + '.svg' : '/img/' + c.icon }) : null;
      const look = el('button', { class: 'mini', title: 'Icona e sfondo del canale', onclick: () => window.intercomLook.open({
        title: 'Aspetto di ' + c.name, withIcon: true, icon: c.icon || '', look: c.look || {},
        onSave: (ch) => postLook(Object.assign({ id: c.id }, ch), 'Aspetto del canale salvato'),
      }) }, icon || '🎨');
      ct.append(el('tr', {}, el('td', {}, look), el('td', {}, name), el('td', {}, mic), el('td', {}, video), pwd, el('td', {}, del)));
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

  // Misure dei riquadri della console, trascinando le maniglie; restano salvate nel browser.
  function setupSplitters() {
    const desk = document.querySelector('.desk');
    const v = $('splitV'), h = $('splitH');
    if (!desk || !v || !h) return;
    const KEY = 'regia.misure';
    let size = {};
    try { size = JSON.parse(localStorage.getItem(KEY)) || {}; } catch (_) {}
    const apply = () => {
      desk.style.removeProperty('--side-w'); desk.style.removeProperty('--top'); desk.style.removeProperty('--bottom');
      if (size.side) desk.style.setProperty('--side-w', size.side + 'px');
      if (size.top) { desk.style.setProperty('--top', size.top + 'fr'); desk.style.setProperty('--bottom', (100 - size.top) + 'fr'); }
    };
    const save = () => { try { localStorage.setItem(KEY, JSON.stringify(size)); } catch (_) {} };
    const drag = (handle, cls, move) => {
      handle.addEventListener('pointerdown', (e) => {
        e.preventDefault();
        handle.setPointerCapture(e.pointerId);
        handle.classList.add('drag');
        document.body.classList.add('resizing', cls);
        const onMove = (ev) => { move(ev); apply(); };
        const onUp = () => {
          handle.removeEventListener('pointermove', onMove);
          handle.classList.remove('drag');
          document.body.classList.remove('resizing', cls);
          save();
        };
        handle.addEventListener('pointermove', onMove);
        handle.addEventListener('pointerup', onUp, { once: true });
        handle.addEventListener('pointercancel', onUp, { once: true });
      });
    };
    drag(v, 'col', (e) => {
      const r = desk.getBoundingClientRect();
      // colonna sinistra tra 200 px e metà schermo
      size.side = Math.round(Math.max(200, Math.min(r.width * 0.5, e.clientX - r.left - 12)));
    });
    drag(h, 'row', (e) => {
      const c = $('config').getBoundingClientRect(), i = document.querySelector('.desk > .intercom').getBoundingClientRect();
      const total = i.bottom - c.top - 12;
      // ognuno dei due riquadri resta almeno al 12% dell'altezza
      size.top = Math.round(Math.max(12, Math.min(88, (e.clientY - c.top - 6) / total * 100)) * 10) / 10;
    });
    v.addEventListener('dblclick', () => { delete size.side; apply(); save(); });
    h.addEventListener('dblclick', () => { delete size.top; apply(); save(); });
    apply();
  }

  setupSplitters();
  setupFileActions();
  buildMobileBar();
  load();
})();
