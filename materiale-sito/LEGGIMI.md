# CHARLIE26 – materiale per il sito www.charlie26inter.com

## Cosa c'è

| Cartella | Contenuto |
|---|---|
| `testi/` | I testi della landing page, **un file per lingua** (`testi-sito-it.md`, `-en`, `-fr`, `-es`, `-ar`, `-zh`), già divisi per sezione, con le voci del menu, le ancore, i pulsanti e i link di download. Si aprono con qualsiasi editor di testo. |
| `immagini/` | Logo (chiaro e scuro), icona, schermate vere del programma (console di regia, telefono, pop-up, app Android, regia da smartphone). |
| `pdf/` | Le brochure pronte da stampare o da mettere in download: A4 verticale in 6 lingue e A4 orizzontale in italiano. |

## Come montarlo in WordPress (sito a pagina unica)

1. **Una sola pagina** (es. "Home"): *Impostazioni → Lettura → La homepage mostra: una pagina statica* → scegli la pagina.
2. **Una sezione = un blocco "Gruppo"**, nell'ordine dei file di testo: Apertura, Come funziona, Ruoli, Configurazione, Video, App, Regia da smartphone, Download (e, se vuoi, Domande frequenti).
3. **Ancore per il menu**: seleziona il Gruppo → pannello a destra → *Avanzate* → **Ancoraggio HTML** e scrivi il nome indicato nel file (`come-funziona`, `ruoli`, `configurazione`, `video`, `app`, `regia-smartphone`, `download`). Usa le **stesse ancore in tutte le lingue**, così il menu funziona uguale ovunque.
4. **Menu**: *Aspetto → Menu* (o *Editor → Navigazione* nei temi a blocchi) → aggiungi **Link personalizzati** con URL `#come-funziona`, `#ruoli`, … e come testo le voci del file della lingua.
5. **Pulsanti di download**: usa i link indicati nel file; puntano sempre all'ultima versione pubblicata su GitHub.

## Traduzioni con la bandiera

Il modo più semplice e gratuito è il plugin **Polylang**:

1. *Plugin → Aggiungi nuovo → "Polylang" → Installa → Attiva*.
2. *Lingue → Lingue*: aggiungi Italiano (predefinita), English, Français, Español, العربية, 中文 (中国).
3. Nella pagina italiana, nel riquadro **Lingue**, premi **+** accanto a ogni lingua: crea la pagina tradotta, collegata all'originale. Incolla i testi del file di quella lingua, con la stessa struttura e le stesse ancore.
4. *Lingue → Impostazioni → Pagina iniziale*: ogni lingua ha la sua home.
5. **Selettore con bandiere**: in *Aspetto → Menu* compare il box **Selettore lingua**. Aggiungilo al menu e spunta "Mostra le bandiere" (puoi anche nascondere i nomi). Nei temi a blocchi c'è il blocco **Language switcher**.
6. **Arabo**: WordPress imposta da solo la scrittura da destra a sinistra quando la lingua della pagina è l'arabo.
7. Ogni menu va creato per ogni lingua: Polylang lo chiede in *Aspetto → Menu*, con una posizione per lingua.

## Grafica (come la brochure)

- **Colori**: petrolio scuro `#0D4A5F` (titoli), petrolio `#1E6E78`, arancio `#E2621A` (numeri e accenti), fondo chiaro `#F4F7F7`, schede bianche `#FFFFFF` con bordo `#D3DFDF`, testo `#102127`, testo secondario `#3A4E54`.
- **Caratteri** (Google Fonts): **Barlow Condensed** 800 per i titoli, **Source Sans 3** per il testo. Per l'arabo **IBM Plex Sans Arabic** e **Noto Kufi Arabic** per i titoli; per il cinese **Noto Sans SC**.
- **Schede** con angoli arrotondati (14 px), titoletti in maiuscoletto spaziato petrolio sopra i titoli ("IL PROGETTO", "USO IN EVENTO"…), numeri grandi in arancio per i passaggi.

## Da sapere

- Le **domande frequenti** esistono solo in italiano (in fondo a `testi-sito-it.md`): se le vuoi anche nelle altre lingue, vanno tradotte.
- Le traduzioni sono state fatte senza revisione di madrelingua: falle rileggere, soprattutto **arabo** e **cinese**.
- Il canvas con le brochure modificabili è su claude.ai (link nella conversazione).

---
CHARLIE26 – Intercom Evoluto · Andrea Battaglioli – www.andreabattaglioli.it · ABit/s · Made in Bolognina
