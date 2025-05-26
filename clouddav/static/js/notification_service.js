// static/js/notification_service.js
// Handles toast notifications.

(function() {
    function showToast(message, type = 'info', duration = 5000) { // Default duration 5 secondi
        const toastContainer = document.getElementById('toast-container');
        if (!toastContainer) {
            console.error('NotificationService - Toast container not found.');
            console.log(`[Toast ${type.toUpperCase()}]: ${message}`); // Fallback
            return;
        }

        const toast = document.createElement('div');
        toast.classList.add('toast', `toast-${type}`); // Aggiunge la classe base e quella del tipo
        toast.textContent = message;

        // Aggiunge il toast al contenitore. Se flex-direction è column-reverse,
        // i nuovi toast appaiono in alto (visivamente)
        toastContainer.appendChild(toast);
        
        // Forza un reflow per permettere all'animazione CSS di partire
        // Non è più necessario aggiungere la classe .show con un timeout,
        // l'animazione 'toast-pop-rotate' si occupa dell'apparizione.
        // La classe .show non è più usata per l'animazione di entrata.

        // Rimuove il toast dopo la durata specificata (animazione + tempo di visualizzazione)
        // L'animazione di fade-out è gestita da CSS.
        // Dobbiamo solo rimuovere l'elemento dal DOM dopo che l'animazione di fade-out è completa.
        // La durata dell'animazione di fade-out è 0.5s, quindi aspettiamo duration + 500ms.
        setTimeout(() => {
            // L'animazione toast-fade-out dovrebbe aver già nascosto il toast.
            // Qui lo rimuoviamo dal DOM.
            if (toast.parentNode) {
                toast.remove();
            }
        }, duration); // La durata include il tempo di visualizzazione + l'animazione di fade-out
    }

    window.showToast = showToast;
    console.log('notification_service.js loaded');
})();
