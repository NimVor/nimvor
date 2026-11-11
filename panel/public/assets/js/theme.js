// Applies the saved colour theme before first paint (loaded in <head>).
(function () {
    'use strict';
    var theme = null;
    try { theme = localStorage.getItem('nim-theme'); } catch (e) { /* storage unavailable */ }
    if (!theme) {
        // The account's own choice (Appearance), when it is light or dark rather than "match my device".
        var pref = document.documentElement.getAttribute('data-nim-mode');
        if (pref === 'light' || pref === 'dark') { theme = pref; }
    }
    if (!theme) {
        theme = window.matchMedia && window.matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light';
    }
    document.documentElement.setAttribute('data-bs-theme', theme);
    // A sidebar the user hid on a wide screen stays hidden (no flash).
    try { if (localStorage.getItem('nim-sidebar-hidden') === '1') { document.documentElement.classList.add('nim-sidebar-collapsed'); } } catch (e) { /* storage unavailable */ }
})();
