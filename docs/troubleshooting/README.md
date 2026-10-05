# Troubleshooting

### "The WhaCP agent is not reachable"
- `systemctl status whacp-agent` and `journalctl -u whacp-agent -n 50`.
- The socket must exist: `ls -l /run/whacp/agent.sock` → `srw-rw---- root whacp`.
- The panel user must be in the socket group: `id whacp`.

### "The panel could not authenticate with the WhaCP agent"
- Both sides must read the same `/etc/whacp/agent.token` (0640 root:whacp).
- After rotating the token restart both services.
- The agent log shows `rejected request with invalid token` or
  `rejected connection from unauthorised local user` with the UID.

### Agent does not start: "token file must not be world-readable"
`chmod 0640 /etc/whacp/agent.token`.

### I cannot sign in
- After 5 failures for a user (or 10 from an IP) sign-in is locked for 15 minutes.
- Reset a password: `sudo -u whacp WHACP_CONFIG=/etc/whacp/panel.php php /opt/whacp/panel/bin/whacp user:password <user>`.
- Signing in over plain HTTP from a non-localhost address with
  `secure_cookie => true` fails silently in browsers — use the SSH tunnel or HTTPS.

### A website is missing from the list
- Catch-all server blocks (`server_name _`) are counted, not listed.
- Check *Discovery → Discovery notes*: includes outside `/etc/nginx`,
  unreadable or unparsable files are reported there.
- Sites in `sites-available` that are not enabled are shown as *disabled*.

### Databases show "data directory is not readable"
The agent needs `CAP_DAC_READ_SEARCH` (granted by the shipped unit). If you
run it manually as a non-root user, schemas cannot be listed.

### An error page shows a reference code
Search the panel log for it: `grep <REFERENCE> /var/log/whacp/panel.log`.

## Component installation fails

See [Package management and component installation](../architecture/package-management.md): the queue window and the job log show the diagnosis (category, package, the package manager's own output) and what WhaCP already tried.
