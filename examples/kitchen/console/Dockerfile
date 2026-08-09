FROM node:22-alpine AS build
WORKDIR /src/examples/kitchen/console
COPY examples/kitchen/console/package.json examples/kitchen/console/package-lock.json ./
RUN npm ci
COPY examples/kitchen/console/ ./
RUN npm run build

FROM nginx:1.27-alpine
COPY --from=build /src/examples/kitchen/console/dist /usr/share/nginx/html
COPY examples/kitchen/console/nginx.conf.template /etc/nginx/templates/default.conf.template
ENV KITCHEN_URL=http://kitchen:9400
EXPOSE 8083
