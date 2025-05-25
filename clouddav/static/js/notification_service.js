// static/js/notification_service.js
// Handles toast notifications.

(function() {
    function showToast(message, type = 'info', duration = 5000) {
        const toastContainer = document.getElementById('toast-container');
        if (!toastContainer) {
            console.error('NotificationService - Toast container not found. Please add <div id="toast-container"></div> to index.html');
            console.log(`[Toast ${type.toUpperCase()}]: ${message}`); // Fallback
            return;
        }

        const toast = document.createElement('div');
        toast.classList.add('toast', `toast-${type}`);
        toast.textContent = message;

        toastContainer.appendChild(toast);
        
        // Force reflow for transition
        void toast.offsetWidth; 

        toast.classList.add('show');
        setTimeout(() => {
            toast.classList.remove('show');
            toast.addEventListener('transitionend', () => {
                if (toast.parentNode) { // Check if still in DOM
                    toast.remove();
                }
            }, { once: true });
        }, duration);
    }

    // Expose showToast globally
    window.showToast = showToast;
    console.log('notification_service.js loaded');
})();
