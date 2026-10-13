// AudioWorklet di riduzione del rumore con RNNoise.
// Il server antepone a questo file la libreria rnnoise-sync.js, che
// definisce createRNNWasmModuleSync.
//
// RNNoise lavora a blocchi di 480 campioni (10 ms a 48 kHz), il browser
// ne fornisce 128 alla volta: accumuliamo in ingresso e restituiamo in uscita
// con un ritardo massimo di 10 ms.

const FRAME = 480;
const RING = FRAME * 4;
const SCALE = 32768; // RNNoise vuole campioni nella scala dei 16 bit

class RnnoiseProcessor extends AudioWorkletProcessor {
  constructor() {
    super();
    this.mod = createRNNWasmModuleSync();
    this.ptr = this.mod._malloc(FRAME * 4);
    this.ctx = this.mod._rnnoise_create();
    this.inBuf = new Float32Array(FRAME);
    this.inLen = 0;
    this.out = new Float32Array(RING);
    this.outRead = 0;
    this.outWrite = 0;
    this.outLen = 0;
    this.enabled = true;
    this.port.onmessage = (e) => {
      if (e.data && typeof e.data.enabled === 'boolean') this.enabled = e.data.enabled;
    };
  }

  processFrame() {
    const heap = this.mod.HEAPF32; // rileggere: la memoria può essere riallocata
    const base = this.ptr >> 2;
    for (let i = 0; i < FRAME; i++) heap[base + i] = this.inBuf[i] * SCALE;
    if (this.enabled) this.mod._rnnoise_process_frame(this.ctx, this.ptr, this.ptr);
    for (let i = 0; i < FRAME; i++) {
      this.out[this.outWrite] = this.mod.HEAPF32[base + i] / SCALE;
      this.outWrite = (this.outWrite + 1) % RING;
    }
    this.outLen = Math.min(RING, this.outLen + FRAME);
  }

  process(inputs, outputs) {
    const input = inputs[0] && inputs[0][0];
    const output = outputs[0] && outputs[0][0];
    if (!output) return true;
    if (input) {
      let i = 0;
      while (i < input.length) {
        const n = Math.min(FRAME - this.inLen, input.length - i);
        this.inBuf.set(input.subarray(i, i + n), this.inLen);
        this.inLen += n;
        i += n;
        if (this.inLen === FRAME) {
          this.processFrame();
          this.inLen = 0;
        }
      }
    }
    for (let k = 0; k < output.length; k++) {
      if (this.outLen > 0) {
        output[k] = this.out[this.outRead];
        this.outRead = (this.outRead + 1) % RING;
        this.outLen--;
      } else {
        output[k] = 0;
      }
    }
    for (let ch = 1; ch < outputs[0].length; ch++) outputs[0][ch].set(output);
    return true;
  }
}

registerProcessor('rnnoise', RnnoiseProcessor);
