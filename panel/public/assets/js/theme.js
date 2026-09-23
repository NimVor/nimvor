// Applies the saved colour theme before first paint (loaded in <head>).
(function () {
    'use strict';
    var theme = null;
    try { theme = localStorage.getItem('wcp-theme'); } catch (e) { /* storage unavailable */ }
    if (!theme) {
        theme = window.matchMedia && window.matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light';
    }
    document.documentElement.setAttribute('data-bs-theme', theme);
})();
