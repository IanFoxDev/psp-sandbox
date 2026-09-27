# PHP image for the example apps. FrankenPHP serves requests in parallel threads,
# so the copies of a duplicate callback really run at the same time. The PHP
# built-in server does not guarantee that even with PHP_CLI_SERVER_WORKERS: one
# worker may accept all copies and handle them one by one.
FROM dunglas/frankenphp:1-php8.4
RUN install-php-extensions pdo_pgsql zip \
    # Let real environment variables reach $_SERVER, so Symfony prefers them over .env.
    && echo 'variables_order = "EGPCS"' > "$PHP_INI_DIR/conf.d/zz-examples.ini"
COPY --from=composer:2 /usr/bin/composer /usr/bin/composer
ENV FRANKENPHP_CONFIG="num_threads 16"
