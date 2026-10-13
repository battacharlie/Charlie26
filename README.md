# Intercom di produzione

Intercom per eventi che funziona dal browser dello smartphone. Si lancia un solo programma sul computer di regia, e i tecnici inquadrano un QR code. Non devono installare niente e non serve internet.

> **Stato: Fase 3**: canali e logica intercom, ruoli e permessi, libero accesso o accesso con credenziali, configurazione dalla console, riduzione rumore forte.

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
3. Si apre la finestra nera con gli indirizzi e, nel browser, la **console di regia**.

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

## Modalità di accesso

Si sceglie nella **console di regia**, sezione *Configurazione evento*. La configurazione (canali, utenti, modalità) viene salvata da sola nel file `evento.json` della cartella dati e ritrovata al riavvio.

### Libero accesso (predefinita)
- Un **QR unico** per tutti. Chi entra scrive il nome e sceglie il canale.
- Chiunque può **creare un sottocanale** (anche protetto da password) e ne diventa **caporeparto**. Il telefono se lo ricorda: ricaricando la pagina si rientra da caporeparto senza rimettere la password.
- La regia può **nominare caporeparto** un tecnico dalla lista dei collegati, per esempio se chi ha creato il canale va via.

### Accesso con credenziali
- La regia crea gli **utenti** (nome utente, nome mostrato, password), con ruolo **Tecnico** o **Caporeparto** e i canali assegnati.
- Il telefono mostra il **login**. Ruolo e canali li decide la regia.
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

Cartella dati predefinita: `%AppData%\IntercomRegia` su Windows e `~/Library/Application Support/IntercomRegia` su Mac. Contiene `evento.json` (configurazione; delle password è salvata solo un'impronta non reversibile, non la password in chiaro) e la sottocartella `certificati`. Per ripartire da un evento vuoto cancella `evento.json`; per ricreare il certificato cancella `certificati`.

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
