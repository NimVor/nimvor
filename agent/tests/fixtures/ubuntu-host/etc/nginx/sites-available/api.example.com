server {
    listen 443 ssl;
    server_name api.example.com;
    ssl_certificate /etc/ssl/whacp-test/selfsigned.pem;
    ssl_certificate_key /etc/ssl/whacp-test/selfsigned.key;
    location / {
        proxy_pass http://127.0.0.1:3000;
        proxy_set_header Host $host;
    }
}
