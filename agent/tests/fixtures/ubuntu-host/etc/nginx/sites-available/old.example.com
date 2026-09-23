server {
    listen 80;
    server_name old.example.com;
    root /var/www/old.example.com;
}
