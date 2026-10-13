# Intercom di produzione

Intercom per eventi che funziona dal browser dello smartphone. Si lancia un solo programma sul computer di regia, e i tecnici inquadrano un QR code. Non devono installare niente e non serve internet.

> **Stato: Fase 5**: canali e logica intercom, ruoli e permessi, libero accesso o credenziali, console di regia completa (rete, QR stampabili, modelli di evento), **video** (dalla regia, da OBS e dai telefoni, con consenso e qualità selezionabile), avatar, riduzione rumore forte.

## Come funziona

```
📱 Telefono ──voce──▶ 💻 Computer di regia ──voce──▶ 📱 altri telefoni
                       (intercom.exe: riceve ogni voce
                        e la smista a chi deve sentirla)
```

- Il computer di regia fa da centralino (SFU WebRTC). Le voci non vengono mescolate: ogni flusso viene inoltrato così com'è, per avere la latenza più bassa possibile.
- Le pagine sono in **HTTPS**, perché senza HTTPS i browser bloccano il microfono.
- Tutto il traffico audio passa da **una sola porta UDP** (predefinita 8443).

## Scaricare il programma

Su GitHub apri **Actions → Compila**, poi l'esecuzione più recente, e in fondo scarica **intercom**. Dentro trovi:

| File | Per |
|---|---|
| `intercom-windows-amd64.exe` | Windows 10/11 |
| `intercom-darwin-arm64` | Mac con chip Apple (M1, M2, M3…) |
| `intercom-darwin-amd64` | Mac con processore Intel |

### Primo avvio su Windows
1. Doppio clic su `intercom-windows-amd64.exe`. Se compare "Windows ha protetto il PC", scegli **Ulteriori informazioni → Esegui comunque**.
2. Al primo avvio Windows chiede se consentire l'accesso alla rete: spunta **Reti private** e premi **Consenti**. Senza questo passaggio i telefoni non si collegano.
3. Si apre nel browser la **console di regia**. Non compare nessuna finestra nera: il programma resta attivo come **icona vicino all'orologio** (un microfono bianco su un tondo rosso; se non la vedi, cercala sotto la freccetta ˄).
   - **clic sinistro** sull'icona: riapre la console;
   - **clic destro**: *Apri console di regia*, *Apri cartella dati*, *Esci (spegne l'intercom)*.
4. Se fai di nuovo doppio clic sul programma già acceso, si apre semplicemente la sua console.
5. Se qualcosa impedisce l'avvio (per esempio una porta occupata), compare una finestrella con la spiegazione.

### Primo avvio su Mac
I file non sono firmati da Apple, quindi va fatto un passaggio in più:
1. apri il Terminale nella cartella del file;
2. `chmod +x intercom-darwin-arm64` (oppure `-amd64`);
3. `xattr -d com.apple.quarantine intercom-darwin-arm64`;
4. `./intercom-darwin-arm64`.

## Collegare i telefoni

1. Telefono e computer di regia devono stare **sulla stessa rete** (stesso Wi-Fi o switch).
2. Inquadra il **QR per i tecnici** mostrato nella console.
3. La prima volta compare l'avviso "connessione non privata". Si risolve in due modi:
   - **veloce**: Android: *Avanzate → Procedi*; iPhone: *Mostra dettagli → visita questo sito web*;
   - **definitivo**: inquadra il **QR di aiuto** e installa il certificato della regia (istruzioni nella pagina).
4. Scrivi il nome, premi **Entra** e rispondi **Consenti** al microfono.

**Pulsante microfono:** un tocco breve lo apre e lo chiude. Se lo tieni premuto, parli finché non lo rilasci (push-to-talk).

## Console di regia

Si apre da sola all'avvio (`https://localhost:8443/regia`) ed è pensata per stare in uno schermo 16:9:
- **a sinistra**: QR per i tecnici, QR di aiuto per il certificato, **rete usata nei QR** (se il PC ha più schede di rete, si sceglie qui; la scelta viene ricordata), **anteprime video** (qualità bassa, massimo 15 fps; clic = grande in qualità alta);
- **in alto a destra, Configurazione**, con le schede:
  - *Evento*: nome, modalità di accesso, **modelli di evento** (salva la configurazione con un nome, ricaricala con un clic), **Esporta / Importa** su file `.json` (per prepararla su un altro computer), **Spegni il programma**;
  - *Canali*, *Utenti*;
  - *Video*: invio di periferiche del browser, indirizzo e chiave per OBS, richieste da approvare (vedi sotto);
  - *QR*: QR generale, **QR per canale** (libero accesso: l'app propone già quel canale) o **QR personale per utente** (credenziali: si entra senza password), ognuno scaricabile in PNG; **Stampa / salva PDF** apre un foglio A4 con le schede da ritagliare;
  - *Registro*: i messaggi del programma, salvati anche in `intercom.log` nella cartella dati;
- **in basso a destra, Intercom**: tasti PARLA per canale, PARLA A TUTTI, lista dei collegati.

## Video

Ci possono essere **più video contemporaneamente**, ognuno destinato a uno o più canali.

| Chi | Da dove | Dove arriva |
|---|---|---|
| **Regia** | console, scheda *Video*: una periferica del browser (scheda di acquisizione, webcam, **OBS Virtual Camera**, **vMix Video**). Se ne possono avviare più di una | nei canali scelti, **subito**; in ogni canale solo se il caporeparto ha attivato *Video della regia* (⚙ o scheda Canali) |
| **OBS** (WHIP) | OBS ≥ 30: *Impostazioni → Trasmissione → Servizio "WHIP"*, con l'indirizzo e la chiave (Bearer Token) mostrati nella scheda *Video* | come il video della regia; i canali si scelgono dalla console |
| **Caporeparto** | telefono, **📹 Mostra video** (fotocamera posteriore o frontale) | **subito** nei canali di cui è caporeparto; in Produzione serve il consenso della regia |
| **Tecnico** | telefono, **📹 Mostra video** | parte solo quando un caporeparto di quel canale (o la regia) preme **Approva**. Chi deve decidere riceve un avviso e vede l'anteprima |

- **Fermare un video**: chi lo invia, il caporeparto del canale o la regia. Il video della regia lo ferma solo la regia; il caporeparto può solo non farlo arrivare nel suo canale.
- **Qualità** (telefono, sopra i video): **Alta**, **Bassa** (circa 360p, 15 fps: consuma poco) o **Spento**. Tocca un video per vederlo a schermo intero.
- I video dal browser partono sempre in due versioni, alta e bassa, e il server manda a ognuno quella scelta, cambiando senza interruzioni. OBS manda una sola versione: chi guarda riceve sempre quella.
- **vMix**: il modo più semplice è l'uscita *Virtual Camera* di vMix, da scegliere nella scheda *Video* della console.
- Latenza tipica in rete locale: qualche decimo di secondo.

## Avatar
All'ingresso ognuno sceglie un avatar: uno dei quattro mostriciattoli (Bit, Byte, Pixel, Glitch) oppure una **foto** (📷). La foto viene ritagliata e ridotta sul telefono a pochi KB prima dell'invio. Si cambia anche da collegati, toccando il proprio avatar in alto.

## Modalità di accesso

Si sceglie nella **console di regia**, sezione *Configurazione evento*. La configurazione (canali, utenti, modalità) viene salvata da sola nel file `evento.json` della cartella dati e ritrovata al riavvio.

### Libero accesso (predefinita)
- Un **QR unico** per tutti. Chi entra scrive il nome e sceglie il canale.
- Chiunque può **creare un sottocanale** (anche protetto da password) e ne diventa **caporeparto**. Il telefono se lo ricorda: ricaricando la pagina si rientra da caporeparto senza rimettere la password.
- La regia può **nominare caporeparto** un tecnico dalla lista dei collegati, per esempio se chi ha creato il canale va via.

### Accesso con credenziali
- La regia crea gli **utenti** (nome utente, nome mostrato, password), con ruolo **Tecnico** o **Caporeparto** e i canali assegnati.
- Il telefono mostra il **login**. Ruolo e canali li decide la regia.
- In alternativa ognuno può inquadrare il proprio **QR personale** (scheda QR della console): compare "Ciao Marco" e basta premere Entra. Il QR contiene una chiave di accesso, non la password. Se un QR va perso, **Nuovo codice** lo rende inutilizzabile.
- Le modifiche valgono subito anche per chi è già collegato: un canale aggiunto compare sul telefono, un utente eliminato viene scollegato.
- Cambiando modalità tutti i collegati vengono scollegati e devono rientrare.

## Ruoli e permessi

| Ruolo | Canali | Poteri |
|---|---|---|
| **Regia** (console) | tutti | parla a uno, a più o a tutti i canali; configura l'evento; scollega chiunque; gestisce ogni canale |
| **Caporeparto** | Produzione + i propri reparti | due tasti PARLA; con ⚙ sul proprio canale gestisce i microfoni degli utenti |
| **Tecnico** | quelli scelti/assegnati | usa il microfono come deciso dal caporeparto |

Con l'ingranaggio ⚙ il caporeparto, e la regia su ogni canale, sceglie come gli utenti usano il microfono:
- **Libero**: tocco per aprire e chiudere, oppure tenere premuto per parlare;
- **Solo push-to-talk**: si parla solo tenendo premuto;
- **Solo il caporeparto apre**: il tecnico non può aprirsi il microfono; ci pensa il caporeparto con **Apri mic** e **Chiudi mic**. Sul telefono del tecnico compare "APERTO DAL CAPOREPARTO".

Il caporeparto può anche **chiudere il microfono** di un singolo utente in qualsiasi momento, e decidere se nel canale arriva il **video della regia** (attivo dalla Fase 5).

**Chi sente chi:** si sente chi sta parlando su un canale che si ascolta. Il caporeparto video che parla in Produzione lo sentono la regia e gli altri caporeparto, ma non i suoi cameraman. Ogni riga canale mostra chi sta parlando e ha il tasto **🔊** per smettere di ascoltare quel canale. Chi ha più canali ha anche il volume per canale; su iPhone il volume della pagina viene ignorato e conta solo quello del telefono.

**Console:** i tasti **1…9** parlano al canale corrispondente, **Spazio** a tutti. Tocco breve per agganciare, tenere premuto per parlare. Il pannello di regia e la configurazione funzionano solo sul computer di regia (`https://localhost…`).

Canali iniziali di un evento nuovo: Produzione, Video, Audio, Luci. Si cambiano dalla console oppure, solo al primo avvio, con `-principale` e `-canali`.

### Riduzione rumore forte
L'interruttore **Riduzione rumore forte** attiva RNNoise, lo stesso filtro di Jitsi Meet. Serve vicino al palco, in sala macchine o dentro un camion regia. Nelle prove ha tolto altri ~30 dB di rumore di fondo rispetto al filtro normale del browser. Aggiunge 10 ms di ritardo e consuma un po' più batteria. La scelta resta memorizzata sul telefono.

## Opzioni (avanzate)

Si passano da riga di comando, per esempio `intercom.exe -ip 192.168.1.10`:

| Opzione | Predefinito | Cosa fa |
|---|---|---|
| `-ip` | automatico | IP da usare nei QR (se il PC ha più schede di rete) |
| `-porta` | 8443 | porta HTTPS |
| `-porta-udp` | 8443 | porta UDP per l'audio |
| `-porta-aiuto` | 8080 | pagina di aiuto in HTTP (0 = disattiva) |
| `-dati` | cartella utente | dove salvare certificati ed eventi |
| `-principale` | Produzione | nome del canale principale (solo per un evento nuovo) |
| `-canali` | Video,Audio,Luci | reparti, separati da virgola (solo per un evento nuovo) |
| `-cert`, `-key`, `-dominio` | – | certificato di un dominio vero (opzione "nessun avviso") |

Cartella dati predefinita: `%AppData%\IntercomRegia` su Windows e `~/Library/Application Support/IntercomRegia` su Mac. Contiene `evento.json` (configurazione; delle password è salvata solo un'impronta non reversibile, non la password in chiaro), `intercom.log` (registro dell'ultima esecuzione; quello precedente è `intercom.log.1`), `impostazioni.json` (rete scelta), la cartella `modelli` e la sottocartella `certificati`. Per ripartire da un evento vuoto cancella `evento.json`; per ricreare il certificato cancella `certificati`.

### Certificato di dominio (nessun avviso sui telefoni)
Se hai un certificato valido per un dominio (es. wildcard `*.intercom.tuodominio.it`) e un DNS che risolve `192-168-1-10.intercom.tuodominio.it` su `192.168.1.10`:

```
intercom.exe -cert fullchain.pem -key privkey.pem -dominio intercom.tuodominio.it
```

In alternativa copia i file come `dominio.crt` e `dominio.key` nella cartella `certificati` dei dati. La guida completa per ottenerli arriverà più avanti.

## Per sviluppatori

```
go test -race ./...     # test automatici (inclusi due "telefoni" simulati)
go run .                # avvia in locale
```

## Licenze di terze parti

- [RNNoise](https://github.com/xiph/rnnoise) (BSD 3 clausole), nella build WebAssembly [`@jitsi/rnnoise-wasm`](https://github.com/jitsi/rnnoise-wasm) (Apache 2.0): vedi `web/rnnoise/`.
- [Pion WebRTC](https://github.com/pion/webrtc) (MIT), [gorilla/websocket](https://github.com/gorilla/websocket) (BSD), [go-qrcode](https://github.com/skip2/go-qrcode) (MIT).
