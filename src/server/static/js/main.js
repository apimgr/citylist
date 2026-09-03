// Service Worker Registration
if ('serviceWorker' in navigator) {
    window.addEventListener('load', () => {
        navigator.serviceWorker.register('/sw.js')
            .then(registration => {
                console.log('SW registered:', registration.scope);
            })
            .catch(error => {
                console.log('SW registration failed:', error);
            });
    });
}

// Format large numbers with commas
function formatNumber(num) {
    return num.toString().replace(/\B(?=(\d{3})+(?!\d))/g, ',');
}

// Initialize number formatting on page load
document.addEventListener('DOMContentLoaded', () => {
    document.querySelectorAll('.stat-value').forEach(el => {
        const num = parseInt(el.textContent.replace(/,/g, ''));
        if (!isNaN(num)) {
            el.textContent = formatNumber(num);
        }
    });
});
