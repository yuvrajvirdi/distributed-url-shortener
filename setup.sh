#!/bin/bash

# install Redis using brew
echo "Installing Redis..."
brew install redis
brew services start redis

# traverse into the nginx folder
cd nginx

# configure Nginx
echo "Configuring Nginx..."
# assuming you have the nginx.conf file in the nginx folder
# you can copy it to the appropriate Nginx configuration path on your system
# for example, on some systems, the path could be /usr/local/etc/nginx/nginx.conf
cp nginx.conf /usr/local/etc/nginx/nginx.conf
nginx -s reload

# move back to the original directory
cd ..

# build and run the go application
echo "Building and running the Go application..."
go build
./distributed-url-shortener
