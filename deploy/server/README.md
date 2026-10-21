# CHARLIE26 su un server pubblico (VPS)

Con la modalità server il programma gira su un VPS con un nome vero
(per esempio `intercom.charlie26inter.com`) e un certificato HTTPS valido:
telefoni e regia si collegano **da qualsiasi rete**, senza avvisi di sicurezza
e senza installare nessun certificato.

## Cosa serve
- Un VPS Linux con IP pubblico (provato su Rocky Linux 9; vale anche AlmaLinux 9).
- Un record DNS **A** (`intercom` → IP del VPS).
- Le porte aperte dal firewall (lo script le apre da solo): **TCP 80, TCP 443, UDP 443**, più SSH.

## Installazione
Collegati al server come amministratore e lancia (da root):

```
curl -fsSLO https://raw.githubusercontent.com/battacharlie/Charlie26/main/deploy/server/installa-rocky.sh
bash installa-rocky.sh intercom.charlie26inter.com tua@email.it
```

Lo script chiede la **password della regia** (almeno 12 caratteri), installa certbot e
ottiene il certificato, scarica il programma e ne verifica l'impronta SHA-256, crea il
servizio `charlie26` e configura il firewall. Il certificato si rinnova da solo e il
programma lo ricarica senza fermarsi.

## Uso
- Console di regia: `https://intercom.charlie26inter.com/regia` → **Entra con la password**.
- Tecnici: `https://intercom.charlie26inter.com/` o i QR code della console.
- Messaggi del programma: `journalctl -u charlie26 -f`
- Aggiornare: rilancia lo script (tiene password e dati) oppure sostituisci `/usr/local/bin/charlie26` e fai `systemctl restart charlie26`.

## Come funziona la sicurezza
- In modalità server **nessuno è "la regia" solo perché arriva da localhost**: la regia entra con la password
  (o con un telefono abbinato dalla console). Chi sbaglia la password 5 volte viene bloccato 15 minuti.
- Il programma non parte se il certificato non copre il nome indicato.
- La password sta in `/etc/charlie26/charlie26.env` (solo root); la passa il servizio al programma con la
  variabile d'ambiente `CHARLIE26_PASSWORD_REGIA`.

## Limiti da conoscere
- L'audio/video usa **UDP sulla porta 443**. Alcune reti molto restrittive (certe Wi-Fi aziendali) bloccano l'UDP:
  lì i telefoni si collegano alla pagina ma non sentono l'audio. Non c'è ancora un ripiego su TCP.
- Se il server sta dietro un NAT (IP "mappato") aggiungi `-ip-pubblico IL.TUO.IP` all'`ExecStart` del servizio.
- Tutto il traffico audio/video passa dal VPS: serve banda in uscita proporzionale a quanti guardano il video.
