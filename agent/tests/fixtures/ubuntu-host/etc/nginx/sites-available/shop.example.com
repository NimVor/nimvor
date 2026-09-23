server {
    listen 80;
    server_name shop.example.com;
    root /var/www/shop.example.com/public;
    add_header X-Frame-Options "SAMEORIGIN";
    location ~ \.php$ {
        fastcgi_pass 127.0.0.1:9084;
        fastcgi_param SCRIPT_FILENAME "$document_root$fastcgi_script_name";
    }
}
