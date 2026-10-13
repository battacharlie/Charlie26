# Intercom di produzione

Intercom per eventi che funziona dal browser dello smartphone. Si lancia un solo programma sul computer di regia, e i tecnici inquadrano un QR code. Non devono installare niente e non serve internet.

> **Stato: Fase 2**: canali multipli con logica intercom (Produzione + reparti), tasti "parla" per canale, riduzione rumore forte.

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

## Canali e ruoli

All'ingresso ognuno sceglie il **canale** e il **ruolo**:

| Ruolo | Canali | Tasti "parla" |
|---|---|---|
| **Tecnico** | solo il canale scelto | un grande pulsante rotondo |
| **Caporeparto** | Produzione + il proprio reparto | due tasti separati |
| **Regia** (console) | tutti | uno per canale + **PARLA A TUTTI** |

- Si sente chi sta parlando su un canale che si ascolta. Esempi:
  - il caporeparto video che parla in Produzione lo sentono la regia e gli altri caporeparto, ma non i suoi cameraman;
  - la regia che parla solo al canale Audio la sente solo il reparto audio.
- Ogni riga canale mostra **chi sta parlando** e ha il tasto **🔊** per smettere di ascoltare quel canale. Chi ha più canali ha anche il **volume per canale**. Su iPhone il volume della pagina viene ignorato e conta solo quello del telefono.
- In console i tasti **1…9** parlano al canale corrispondente e **Spazio** parla a tutti: tocco breve per agganciare, tenere premuto per parlare.
- Il ruolo "Regia" è disponibile solo dalla console aperta sul computer di regia. Login e password arriveranno nella Fase 3.

Canali predefiniti: Produzione, Video, Audio, Luci. Per cambiarli:

```
intercom.exe -principale "Produzione" -canali "Video,Audio,Luci,Palco,Streaming"
```

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
| `-principale` | Produzione | nome del canale principale |
| `-canali` | Video,Audio,Luci | reparti, separati da virgola |
| `-cert`, `-key`, `-dominio` | – | certificato di un dominio vero (opzione "nessun avviso") |

Cartella dati predefinita: `%AppData%\IntercomRegia` su Windows e `~/Library/Application Support/IntercomRegia` su Mac. Per ricreare il certificato da zero, cancella la sottocartella `certificati`.

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
