// Abbinamento di un telefono come regia: la console mostra un QR con
// https://…/regia#abbina=CODICE. Qui il codice si scambia con il cookie
// personale e la pagina si ricarica pulita (il codice vale una volta sola).
(() => {
  'use strict';
  const m = /[#&]abbina=([0-9a-f]{32})/.exec(location.hash);
  if (!m) return;
  window.regiaPairing = true; // la console aspetta il ricaricamento
  history.replaceState(null, '', location.pathname);
  let nome = '';
  try { nome = localStorage.getItem('regia.nomeTelefono') || ''; } catch (_) {}
  nome = (window.prompt('Nome di questo telefono (per riconoscerlo nella console):', nome || 'Telefono regia') || 'Telefono regia').trim();
  try { localStorage.setItem('regia.nomeTelefono', nome); } catch (_) {}
  fetch('/api/regia-remota/abbina', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ codice: m[1], nome }) })
    .then((r) => r.json().then((out) => ({ ok: r.ok, out })))
    .then(({ ok, out }) => {
      if (!ok) throw new Error(out.error || 'Abbinamento non riuscito');
      location.replace('/regia');
    })
    .catch((e) => {
      alert(e.message);
      location.replace('/regia');
    });
})();
