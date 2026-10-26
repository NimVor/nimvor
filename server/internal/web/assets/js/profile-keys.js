// Moved out of the template: the panel's CSP forbids inline scripts.
  (function(){
    var form = document.getElementById('wcp-key-add-form');
    var msg = document.getElementById('wcp-key-add-msg');
    var btn = document.getElementById('wcp-key-add-btn');
    if (!form || !navigator.credentials || !navigator.credentials.create) { btn && (btn.disabled = true); return; }
    function show(text, kind) {
      msg.hidden = false;
      msg.className = 'alert ' + (kind === 'error' ? 'alert-danger' : 'alert-success');
      msg.textContent = text;
    }
    function hide() { msg.hidden = true; }
    function postForm(url, body) {
      var fd = new URLSearchParams();
      fd.set('_token', form._token.value);
      Object.keys(body).forEach(function(k){ fd.set(k, body[k]); });
      return fetch(url, {method:'POST', headers:{'Content-Type':'application/x-www-form-urlencoded','X-CSRF-Token':form._token.value}, body: fd.toString(), credentials:'same-origin'}).then(function(r){return r.json().then(function(j){return {ok:r.ok, body:j};});});
    }
    function postJSON(url, body) {
      return fetch(url, {method:'POST', headers:{'Content-Type':'application/json','X-CSRF-Token':form._token.value}, body: JSON.stringify(body), credentials:'same-origin'}).then(function(r){return r.json().then(function(j){return {ok:r.ok, body:j};});});
    }
    function bufToB64u(buf) {
      var s = btoa(String.fromCharCode.apply(null, new Uint8Array(buf)));
      return s.replace(/\+/g,'-').replace(/\//g,'_').replace(/=+$/,'');
    }
    function b64uToBuf(s) {
      s = s.replace(/-/g,'+').replace(/_/g,'/'); while (s.length%4) s += '=';
      return Uint8Array.from(atob(s), function(c){return c.charCodeAt(0);}).buffer;
    }
    form.addEventListener('submit', function(e) {
      e.preventDefault(); hide();
      var name = document.getElementById('wcp-key-name').value.trim();
      if (!name) { show('Give the key a name.', 'error'); return; }
      btn.disabled = true;
      postJSON('/profile/security-keys/options', {}).then(function(resp){
        if (!resp.ok || !resp.body.success) { show((resp.body.error && resp.body.error.message) || 'Could not start registration.', 'error'); btn.disabled=false; return; }
        var opts = resp.body.data;
        opts.challenge = b64uToBuf(opts.challenge);
        opts.user.id = b64uToBuf(opts.user.id);
        if (opts.excludeCredentials) opts.excludeCredentials = opts.excludeCredentials.map(function(c){ c.id = b64uToBuf(c.id); c.type='public-key'; return c; });
        return navigator.credentials.create({publicKey: opts}).then(function(cred){
          if (!cred) { btn.disabled=false; return; }
          return postForm('/profile/security-keys', {
            name: name,
            client_data: bufToB64u(cred.response.clientDataJSON),
            attestation: bufToB64u(cred.response.attestationObject),
          }).then(function(r2){
            if (r2.ok && r2.body.success) { window.location.href = (r2.body.data && r2.body.data.redirect) || '/profile#security-keys'; return; }
            show((r2.body.error && r2.body.error.message) || 'Key could not be added.', 'error'); btn.disabled = false;
          });
        }).catch(function(e){ show('Touch the key to confirm. (' + (e.message||e) + ')', 'error'); btn.disabled = false; });
      }).catch(function(){ show('Network error.', 'error'); btn.disabled = false; });
    });
  })();
  
