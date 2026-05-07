FROM openresty/openresty:1.27.1.2-alpine

# Install luarocks and build dependencies, then install lua-resty-http
RUN apk add --no-cache luarocks5.1 curl \
    && luarocks-5.1 install lua-resty-http 0.17.2

# Copy Lua application code
COPY lua/ /opt/parparchik/lua/

# Copy nginx configuration
COPY nginx.conf /usr/local/openresty/nginx/conf/nginx.conf

EXPOSE 8080

CMD ["openresty", "-g", "daemon off;"]
