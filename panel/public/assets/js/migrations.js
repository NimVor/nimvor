// Migrations: while a discovery or a migration runs the page looks again every few seconds (unless a window is open).
(function () {
    'use strict';
    var running = document.querySelector('[data-fetched-running="1"]') || document.querySelector('.nim-state .spinner-border');
    if (!running) { return; }
    setInterval(function () {
        if (!document.querySelector('.modal.show') && !document.activeElement.matches('input, textarea, select')) { location.reload(); }
    }, 5000);
})();

// The printable report: the button opens the browser's print dialog (save as PDF from there).
document.addEventListener('click', function (e) {
    var b = e.target.closest ? e.target.closest('[data-nim-print]') : null;
    if (b) { e.preventDefault(); window.print(); }
});
