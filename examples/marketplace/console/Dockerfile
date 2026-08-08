# The merchant console. Built from the repository root, because it consumes the
# TypeScript SDK from client-ts/ as a file dependency — the SDK is not
# published to npm, it moves with the API.
FROM node:22-alpine AS sdk
WORKDIR /src/client-ts
COPY client-ts/package.json client-ts/package-lock.json ./
RUN npm ci
COPY client-ts/ ./
RUN npm run build

FROM node:22-alpine AS build
WORKDIR /src
COPY --from=sdk /src/client-ts ./client-ts
WORKDIR /src/examples/marketplace/console
COPY examples/marketplace/console/package.json examples/marketplace/console/package-lock.json ./
RUN npm ci
COPY examples/marketplace/console/ ./
RUN npm run build

FROM nginx:1.27-alpine
COPY --from=build /src/examples/marketplace/console/dist /usr/share/nginx/html
COPY examples/marketplace/console/nginx.conf.template /etc/nginx/templates/default.conf.template
# nginx's own entrypoint renders /etc/nginx/templates/*.template with envsubst,
# so PLATFORM_URL and PLATFORM_API_TOKEN arrive as environment variables and
# the credential is never baked into an image layer.
ENV PLATFORM_URL=http://platform:9300
EXPOSE 8081
