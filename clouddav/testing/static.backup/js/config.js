// static/js/config.js

// Definisce l'oggetto di configurazione globale per il frontend.
// Questo file dovrebbe essere incluso per primo nel tuo index.html.

window.config = {
    // --- Configurazione dei Livelli di Log ---
    LogLevelDebug: 'DEBUG',
    LogLevelInfo: 'INFO',
    LogLevelWarning: 'WARNING', // Aggiunto per completezza
    LogLevelError: 'ERROR',   // Aggiunto per completezza

    // Imposta il livello di log corrente per l'applicazione.
    // Cambia in 'INFO', 'WARNING', o 'ERROR' per la produzione per ridurre la verbosità.
    currentLogLevel: 'DEBUG', 

    /**
     * Verifica se il livello di log specificato è attivo.
     * @param {string} levelToCheck Es. config.LogLevelDebug, config.LogLevelInfo
     * @returns {boolean} True se il messaggio per questo livello dovrebbe essere loggato.
     */
    IsLogLevel: function(levelToCheck) {
        if (!this.currentLogLevel) return false; // Nessun log se non è impostato

        const levels = {
            [this.LogLevelDebug]: 1,
            [this.LogLevelInfo]: 2,
            [this.LogLevelWarning]: 3,
            [this.LogLevelError]: 4
        };

        const current = levels[this.currentLogLevel];
        const toCheck = levels[levelToCheck];

        if (current === undefined || toCheck === undefined) {
            return false; // Livello sconosciuto
        }

        return toCheck >= current; // Logga se il livello del messaggio è uguale o più severo del livello corrente
    }

    // --- Altre Configurazioni Frontend (Esempi) ---
    // Potresti aggiungere qui altre configurazioni utili per il frontend, ad esempio:
    // defaultItemsPerPage: 50,
    // maxReconnectAttempts: 5,
    // reconnectDelayBaseMs: 2000,
    // requestTimeoutMs: 30000,
    //
    // Queste potrebbero essere usate dai moduli websocket_service.js, data_service.js, ecc.
    // invece di avere costanti hardcoded al loro interno.
    // Per ora, le lasciamo commentate per mantenere la semplicità e risolvere prima
    // i problemi di base.
};

// Log di conferma che config.js è stato caricato e inizializzato
if (window.config && window.config.IsLogLevel && window.config.IsLogLevel(window.config.LogLevelInfo)) {
    console.info("Frontend config.js caricato e inizializzato. Livello di log corrente:", window.config.currentLogLevel);
}
