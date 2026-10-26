// Applies the saved colour theme before first paint (loaded in <head>).
(function () {
    'use strict';
    var theme = null;
    try { theme = localStorage.getItem('wcp-theme'); } catch (e) { /* storage unavailable */ }
    if (!theme) {
        theme = window.matchMedia && window.matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light';
    }
    document.documentElement.setAttribute('data-bs-theme', theme);
    // A sidebar the user hid on a wide screen stays hidden (no flash).
    try { if (localStorage.getItem('wcp-sidebar-hidden') === '1') { document.documentElement.classList.add('wcp-sidebar-collapsed'); } } catch (e) { /* storage unavailable */ }
})();
