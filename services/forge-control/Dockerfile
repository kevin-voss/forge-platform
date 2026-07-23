# syntax=docker/dockerfile:1

FROM eclipse-temurin:21-jdk-jammy AS build
WORKDIR /src

COPY gradlew settings.gradle.kts build.gradle.kts gradle.properties ./
COPY gradle ./gradle
COPY src ./src

RUN chmod +x gradlew \
	&& ./gradlew --no-daemon test jar

FROM eclipse-temurin:21-jre-jammy AS runtime
WORKDIR /app

# docker CLI is required when FORGE_DB_PROVISIONER=local (LocalProvisioner).
# The socket is mounted only in demos/dev overlays that enable the local provisioner.
RUN apt-get update \
	&& apt-get install -y --no-install-recommends curl ca-certificates \
	&& install -m 0755 -d /etc/apt/keyrings \
	&& curl -fsSL https://download.docker.com/linux/ubuntu/gpg -o /etc/apt/keyrings/docker.asc \
	&& chmod a+r /etc/apt/keyrings/docker.asc \
	&& echo "deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/docker.asc] https://download.docker.com/linux/ubuntu jammy stable" \
		> /etc/apt/sources.list.d/docker.list \
	&& apt-get update \
	&& apt-get install -y --no-install-recommends docker-ce-cli \
	&& rm -rf /var/lib/apt/lists/* \
	&& useradd --create-home --uid 10001 --shell /usr/sbin/nologin appuser

COPY --from=build /src/build/libs/forge-control-0.1.0.jar /app/forge-control.jar
# Ship SQL migrations on the filesystem so Flyway can discover them from the fat jar image.
COPY --from=build /src/src/main/resources/db/migration /app/db/migration
# Writable backup archive root for FORGE_DB_BACKUP_TARGET=volume (appuser cannot create /var/forge).
RUN mkdir -p /app/data/db-backups \
	&& chown -R appuser:appuser /app

USER appuser:appuser

EXPOSE 8080

ENV PORT=8080 \
	FORGE_SERVICE_NAME=forge-control \
	FORGE_SERVICE_VERSION=0.1.0 \
	FORGE_LOG_LEVEL=info \
	FORGE_ENV=development \
	FORGE_AUTH_MODE=dev \
	FORGE_SHUTDOWN_GRACE_SECONDS=10 \
	DATABASE_URL=jdbc:postgresql://postgres:5432/forge \
	DATABASE_USER=forge \
	DATABASE_PASSWORD=forge \
	DATABASE_SCHEMA=control \
	DATABASE_POOL_MAX=10 \
	DATABASE_MIGRATE_ON_START=true \
	FORGE_DB_BACKUP_DIR=/app/data/db-backups

ENTRYPOINT ["java", "-jar", "/app/forge-control.jar"]
