FROM node:22.12.0-alpine AS dependencies

WORKDIR /workspace/web
COPY web/package.json web/package-lock.json ./
RUN npm ci --ignore-scripts

FROM dependencies AS development

COPY web/ ./
CMD ["npm", "run", "dev", "--", "--host", "0.0.0.0"]

FROM dependencies AS build

COPY web/ ./
ARG VITE_AUTH_MODE=trusted-header
ARG VITE_NOTEBOOK_ORIGIN=
ENV VITE_AUTH_MODE=${VITE_AUTH_MODE}
ENV VITE_NOTEBOOK_ORIGIN=${VITE_NOTEBOOK_ORIGIN}
RUN npm run build

FROM nginxinc/nginx-unprivileged:1.29-alpine AS runtime

COPY install/docker/dockerFrontend/nginx.conf /etc/nginx/conf.d/default.conf
COPY --from=build /workspace/web/dist /usr/share/nginx/html

EXPOSE 8080
