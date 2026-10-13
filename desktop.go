package main

// desktop raccoglie ciò che serve all'integrazione con il sistema operativo
// (icona nell'area di notifica su Windows, segnali di chiusura altrove).
type desktop struct {
	consoleURL string
	appURL     string
	dataDir    string
	quit       <-chan struct{} // chiuso quando la console chiede lo spegnimento
}
