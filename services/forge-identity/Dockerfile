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

RUN apt-get update \
	&& apt-get install -y --no-install-recommends curl \
	&& rm -rf /var/lib/apt/lists/* \
	&& useradd --create-home --uid 10001 --shell /usr/sbin/nologin appuser

COPY --from=build /src/build/libs/forge-identity-0.1.0.jar /app/forge-identity.jar
# Ship SQL migrations on the filesystem so Flyway can discover them from the fat jar image.
COPY --from=build /src/src/main/resources/db/migration /app/db/migration
RUN chown -R appuser:appuser /app

USER appuser:appuser

EXPOSE 8080

ENV PORT=8080 \
	FORGE_SERVICE_NAME=forge-identity \
	FORGE_SERVICE_VERSION=0.1.0 \
	FORGE_LOG_LEVEL=info \
	FORGE_ENV=development \
	FORGE_SHUTDOWN_GRACE_SECONDS=10 \
	FORGE_IDENTITY_DB_URL=jdbc:postgresql://postgres:5432/forge_identity \
	FORGE_IDENTITY_DB_USER=forge \
	FORGE_IDENTITY_DB_PASSWORD=forge \
	FORGE_IDENTITY_DB_POOL_MAX=10 \
	FORGE_IDENTITY_DB_MIGRATE_ON_START=true

ENTRYPOINT ["java", "-jar", "/app/forge-identity.jar"]
