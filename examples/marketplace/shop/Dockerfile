# The shopper storefront. Built from the repository root so the build context
# matches the other services in this example.
FROM node:22-alpine AS build
WORKDIR /src/examples/marketplace/shop
COPY examples/marketplace/shop/package.json examples/marketplace/shop/package-lock.json ./
RUN npm ci
COPY examples/marketplace/shop/ ./
RUN npm run build

FROM nginx:1.27-alpine
COPY --from=build /src/examples/marketplace/shop/dist /usr/share/nginx/html
COPY examples/marketplace/shop/nginx.conf.template /etc/nginx/templates/default.conf.template
ENV STOREFRONT_URL=http://storefront:9200
EXPOSE 8082
