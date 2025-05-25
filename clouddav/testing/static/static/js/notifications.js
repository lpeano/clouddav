// static/js/notifications.js

function showToast(message, type = 'info', duration = 5000) {
    const toastContainer = document.getElementById('toast-container');
    if (!toastContainer) {
        console.error('Toast container not found. Please add <div id="toast-container"></div> to index.html');
        // Fallback al log della console se il contenitore manca
        console.log(`[Toast ${type.toUpperCase()}]: ${message}`);
        return;
    }

    const toast = document.createElement('div');
    toast.classList.add('toast', `toast-${type}`);
    toast.textContent = message;

    toastContainer.appendChild(toast);
    // Forza il reflow per assicurare che la transizione venga riprodotta
    void toast.offsetWidth; 

    toast.classList.add('show');
    setTimeout(() => {
        toast.classList.remove('show');
        toast.addEventListener('transitionend', () => {
            toast.remove();
        }, { once: true });
    }, duration);
}

// Esponi la funzione globalmente per essere chiamata dagli iframe
window.showToast = showToast;