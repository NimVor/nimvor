// Moved out of the template: the panel's CSP forbids inline scripts.
    (function(){
      var btn = document.getElementById('wcp-key-btn');
      var msg = document.getElementById('wcp-key-msg');
      if (!btn || !navigator.credentials || !navigator.credentials.get) { btn && (btn.style.display='none'); return; }
      function show(text, kind) {
        msg.hidden = false;
        msg.className = 'alert ' + (kind === 'error' ? 'alert-danger' : 'alert-info');
        msg.textContent = text;
      }
      function hide() { msg.hidden = true; }
      function postJSON(url, body) {
        return fetch(url, {method:'POST', headers:{'Content-Type':'application/json','X-CSRF-Token':btn.getAttribute('data-csrf')}, body: JSON.stringify(body), credentials:'same-origin'}).then(function(r){return r.json().then(function(j){return {ok:r.ok, status:r.status, body:j};});});
      }
      function bufToB64u(buf) {
        var s = btoa(String.fromCharCode.apply(null, new Uint8Array(buf)));
        return s.replace(/\+/g,'-').replace(/\//g,'_').replace(/=+$/,'');
      }
      function b64uToBuf(s) {
        s = s.replace(/-/g,'+').replace(/_/g,'/');
        while (s.length % 4) s += '=';
        return Uint8Array.from(atob(s), function(c){return c.charCodeAt(0);}).buffer;
      }
      btn.addEventListener('click', function() {
        btn.disabled = true; hide();
        postJSON('/login/two-factor/key/options', {}).then(function(resp){
          if (!resp.ok || !resp.body.success) {
            var m = (resp.body.error && resp.body.error.message) || 'Could not start security-key sign-in.';
            show(m, 'error'); btn.disabled = false;
            if (resp.body.restart) { setTimeout(function(){ window.location.href = '/login'; }, 1500); }
            return;
          }
          var opts = resp.body.data;
          opts.challenge = b64uToBuf(opts.challenge);
          if (opts.allowCredentials) opts.allowCredentials = opts.allowCredentials.map(function(c){ c.id = b64uToBuf(c.id); c.type='public-key'; return c; });
          return navigator.credentials.get({publicKey: opts}).then(function(assertion){
            if (!assertion) { show('No key responded.', 'error'); btn.disabled = false; return; }
            var body = {
              id: bufToB64u(assertion.rawId),
              client_data: bufToB64u(assertion.response.clientDataJSON),
              authenticator_data: bufToB64u(assertion.response.authenticatorData),
              signature: bufToB64u(assertion.response.signature)
            };
            return postJSON('/login/two-factor/key', body).then(function(r2){
              if (r2.ok && r2.body.success) { window.location.href = (r2.body.data && r2.body.data.redirect) || '/'; return; }
              var mm = (r2.body.error && r2.body.error.message) || 'Key was not accepted.';
              show(mm, 'error'); btn.disabled = false;
              if (r2.body.restart) setTimeout(function(){ window.location.href = '/login'; }, 1500);
            });
          }).catch(function(e){ show('Touch the key to confirm. (' + (e.message||e) + ')', 'error'); btn.disabled = false; });
        }).catch(function(){ show('Network error.', 'error'); btn.disabled = false; });
      });
      // Auto-start when there is no TOTP option (key-only account).
      if (btn.hasAttribute('data-autostart')) { btn.click(); }
    })();
    
