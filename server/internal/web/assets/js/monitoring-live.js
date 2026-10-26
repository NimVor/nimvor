// Moved out of the template: the panel's CSP forbids inline scripts.
(function(){var d=document.getElementById('live');function tick(){fetch('/monitoring/live.json',{credentials:'same-origin'}).then(r=>r.json()).then(j=>{d.textContent=JSON.stringify(j,null,2);}).catch(e=>{d.textContent='error: '+e;});setTimeout(tick,2000);}tick();})();
