// ui.js
// Gestisce gli elementi UI globali come la rotella di caricamento.

// Riferimento all'overlay di caricamento
const loadingOverlay = document.getElementById('loading-overlay');

/**
 * Mostra la rotella di caricamento modale.
 * Impedisce l'interazione con l'interfaccia sottostante.
 */
function showLoadingSpinner() {
    // Controlla se l'elemento overlay esiste
    if (loadingOverlay) {
        // Imposta lo stile display su 'flex' per renderlo visibile e centrare il contenuto
        loadingOverlay.style.display = 'flex';
    }
}

/**
 * Nasconde la rotella di caricamento modale.
 * Ripristina l'interazione con l'interfaccia.
 */
function hideLoadingSpinner() {
    // Controlla se l'elemento overlay esiste
    if (loadingOverlay) {
        // Imposta lo stile display su 'none' per nasconderlo
        loadingOverlay.style.display = 'none';
    }
}

// Allega le funzioni all'oggetto window in modo che possano essere chiamate da altri script o iframes.
// Questo le rende accessibili globalmente all'interno della pagina principale e dagli iframes tramite window.parent.
window.showLoadingSpinner = showLoadingSpinner;
window.hideLoadingSpinner = hideLoadingSpinner;
