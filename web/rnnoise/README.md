# RNNoise (riduzione del rumore)

`rnnoise-sync.js` proviene invariato dal pacchetto npm
[`@jitsi/rnnoise-wasm`](https://github.com/jitsi/rnnoise-wasm) versione 0.2.1
(licenza Apache 2.0, vedi `LICENSE-rnnoise-wasm`). È la libreria
[RNNoise](https://github.com/xiph/rnnoise) di Xiph.Org / Jean-Marc Valin
(licenza BSD a 3 clausole) compilata in WebAssembly, la stessa usata da Jitsi Meet.

`processor.js` è il nostro AudioWorklet che la usa. Il server unisce i due
file al volo in `/static/rnnoise-worklet.js`.

Per aggiornare: `npm pack @jitsi/rnnoise-wasm@<versione>` e copiare `dist/rnnoise-sync.js`.
