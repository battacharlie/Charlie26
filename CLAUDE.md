# Intercom di produzione via WebRTC — specifiche di progetto

Rispondi sempre in **italiano**. L'autore è Andrea, tecnico per eventi e streaming (non uno sviluppatore a tempo pieno): spiega le scelte in modo chiaro e concreto.

## Obiettivo

Un software che riproduce il funzionamento delle **radio intercom** usate in produzione durante un evento:
- un canale principale per i responsabili, che possono parlare con tutti e ascoltare tutti;
- canali di reparto separati (video, audio, luci…), ognuno con un caporeparto che comunica anche con gli altri caporeparto e con il responsabile di produzione.

È un **eseguibile** che si lancia sulla macchina di regia (**Windows**, e se possibile anche **macOS**). Questa macchina è collegata via ethernet e ha una buona potenza di calcolo. I tecnici si collegano dal proprio **smartphone tramite browser**, inquadrando un QR code. Non devono installare nulla.

## Contesto: la versione precedente e i suoi problemi

Esisteva un dispositivo Linux con **Jitsi Meet** ridotto all'osso: scelta del canale, pulsante microfono, scelta della qualità video ricevuta (per vedere il programma in onda dalla regia). Funzionava abbastanza bene, ma aveva questi limiti:
- serviva una macchina Linux dedicata;
- bisognava assegnare un IP fisso a mano;
- **WebRTC non è mai stato attivato**. Probabile causa: i browser permettono l'accesso al microfono solo in un contesto sicuro (HTTPS o localhost), e su IP di rete locale senza certificato valido il microfono viene bloccato. **Questo è il problema n.1 da risolvere bene** (vedi sezione HTTPS);
- non era un programma che si lancia quando serve.

## Architettura consigliata (da confermare con Andrea prima di scrivere molto codice)

- **Linguaggio: Go**, con **Pion WebRTC** come SFU (selective forwarding unit). Il motivo è che Go produce un **singolo eseguibile** senza dipendenze, compilabile per Windows e macOS da qualsiasi sistema, e Pion è un'implementazione WebRTC completa in Go puro.
- **Interfacce web incorporate nell'eseguibile** (`embed`):
  - **Console di regia** (SupAdmin), aperta nel browser della macchina di regia su `https://localhost:<porta>`;
  - **App tecnici** (Admin/User), pagina mobile-first raggiunta tramite QR code.
- **Signaling** via WebSocket sullo stesso server HTTPS.
- **Audio**: Opus, bassa latenza, cancellazione eco e soppressione rumore attive nel browser.
- **Video**: solo in ricezione per i tecnici, con qualità selezionabile (simulcast o layer multipli).
- **Ingresso video della regia**, in ordine di preferenza:
  1. **WHIP** da OBS (OBS ≥ 30 ha l'uscita WHIP nativa), che è lo scenario più realistico in regia;
  2. sorgente video del browser della console (scheda di acquisizione, OBS Virtual Camera).
- **Persistenza**: file locali (JSON o SQLite) in una cartella dati accanto all'eseguibile o nella cartella utente. Eventi salvabili e riapribili come modelli.
- Funziona **interamente in rete locale, senza internet**.

## Rete

- All'avvio il programma **rileva da solo gli IP** di tutte le schede di rete attive e propone quella più probabile (ethernet, rete privata). La regia può cambiarla dalla console.
- I QR code puntano all'IP scelto.
- Porte UDP per WebRTC in un intervallo configurabile. Va gestito il primo avvio su Windows con l'avviso del firewall, spiegando all'utente cosa consentire.
- ICE solo host candidate in LAN (nessun server STUN/TURN esterno necessario).

## HTTPS (decisione critica)

Il microfono sugli smartphone funziona solo in HTTPS. Opzioni:
1. **Predefinita**: al primo avvio il programma genera una propria **CA locale** e un certificato per gli IP rilevati. I tecnici accettano l'avviso del browser una volta sola, oppure installano la CA tramite una pagina di aiuto dedicata. Va verificato che funzioni su **iOS Safari** e **Android Chrome**.
2. **Futura/opzionale**: un dominio reale di Andrea con un certificato wildcard valido che risolve sugli IP locali (stile `192-168-1-10.dominio.it`). Nessun avviso, ma serve internet in fase di preparazione e alcuni router bloccano il DNS rebinding.

Presenta i pro e contro ad Andrea e fagli scegliere prima di implementare.

## Ruoli

| Ruolo | Nome nell'interfaccia | Chi è |
|---|---|---|
| **SupAdmin** | Responsabile di Produzione | La regia, sulla macchina principale |
| **Admin** | Responsabile di Canale | Caporeparto |
| **User** | Utente base | Tecnico |

### SupAdmin
- Crea l'evento e sceglie la modalità: **libero accesso** oppure **accesso con credenziali**.
- Con accesso con credenziali: crea canali, utenti (username/password) e assegna ruoli e canali.
- Con libero accesso: l'evento parte con il canale principale **"Produzione"** (nome predefinito, modificabile). Chiunque può aprire sottocanali protetti da password.
- Ascolta e parla su **tutti** i canali, con tasti "parla" per singolo canale come un pannello intercom reale (parlare a uno, a più canali o a tutti).
- È **l'unico che può inviare il flusso video**, nel canale principale e nei sottocanali (in questi ultimi solo se l'Admin del canale lo attiva).
- Ha una **finestra di configurazione** per tutto quanto sopra.

### Admin
- Parla e ascolta nel canale **Produzione** e nel **proprio canale** (due tasti parla separati).
- Decide se gli utenti del suo canale possono aprire o chiudere da soli il microfono.
- Può silenziare o aprire lui stesso il microfono dei singoli utenti del suo canale.
- Decide se nel suo canale arriva il flusso video inviato dal SupAdmin.

### User
- Fa il login con nome e password (se l'evento lo prevede).
- Partecipa solo ai canali assegnati.
- Usa il microfono secondo quanto permesso dall'Admin (toggle e/o push-to-talk).
- Sceglie la qualità del video ricevuto, quando il video è attivo.

### Libero accesso
- QR code **unico** per tutti.
- Ruoli in autocertificazione: chi entra sceglie il canale e inserisce il proprio nome.
- Comportamento predefinito proposto: **chi crea un sottocanale ne diventa Admin**. Da confermare con Andrea.

## QR code
- Con accesso con credenziali: un QR per ruolo e/o canale (e opzionalmente per singolo utente con credenziali precompilate). Stampabili ed esportabili in PDF/PNG dalla console.
- Con libero accesso: un QR unico.
- Il QR porta alla pagina di accesso ai canali.

## Personalizzazione (modificabile da SupAdmin; da Admin per il proprio canale)
- Sfondo della pagina di accesso e delle pagine di utilizzo: **colore o immagine**.
- **Icona utente** a scelta tra **4 icone predefinite di mostriciattoli "nerd"**. Se Andrea non fornisce le immagini, disegnale come SVG originali, semplici e riconoscibili anche in piccolo.
- **Icona per ogni canale** (stesse 4 più eventuali caricate).

## Interfaccia tecnici (mobile-first)
Essenziale, utilizzabile con una mano e al buio:
- canale/i e chi sta parlando (indicatore visivo);
- grande pulsante microfono (toggle e/o push-to-talk);
- video del programma, se attivo, con scelta della qualità;
- schermo che resta acceso (Wake Lock) e riconnessione automatica.

## Distribuzione
- **GitHub Actions** che compila ad ogni release:
  - `windows/amd64` (.exe);
  - `darwin/arm64` e `darwin/amd64`.
- I binari macOS non saranno firmati: documenta come aprirli (Gatekeeper).
- Un README in italiano per l'uso in evento.

## Fasi di lavoro
1. **Prototipo minimo**: server Go + Pion, un canale, audio bidirezionale da due telefoni in HTTPS sulla LAN. **Risolvere HTTPS/microfono qui.**
2. Canali multipli e logica intercom (Produzione + reparti, tasti parla, ascolto multiplo).
3. Ruoli, permessi, login, modalità libero accesso/credenziali.
4. Console di regia: configurazione evento, rilevamento IP, QR code.
5. Video dalla regia (WHIP da OBS) con qualità selezionabile e abilitazione per canale.
6. Personalizzazione (sfondi, icone).
7. Build Windows/macOS con GitHub Actions, README.

Alla fine di ogni fase: fai commit, spiega ad Andrea cosa provare e come provarlo.

## Regole di lavoro
- Prima di dichiarare finita una cosa, **verificala in più passaggi indipendenti** (compilazione, test automatici, controllo manuale della logica). Riporta cosa hai verificato e cosa invece va provato su hardware reale (telefoni, rete dell'evento).
- Non riscrivere da zero file già funzionanti senza motivo. Ogni cambiamento deve passare da un commit con un messaggio chiaro.
- Quando una scelta di architettura è importante e reversibile solo con fatica, chiedi prima.
