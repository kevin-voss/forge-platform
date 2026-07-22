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

RUN useradd --create-home --uid 10001 --shell /usr/sbin/nologin appuser

COPY --from=build /src/build/libs/demo-kotlin-api-0.1.0.jar /app/demo-kotlin-api.jar
RUN chown -R appuser:appuser /app

USER appuser:appuser

EXPOSE 8080

ENV PORT=8080 \
	FORGE_SERVICE_NAME=demo-kotlin-api \
	FORGE_SERVICE_VERSION=0.1.0 \
	FORGE_LOG_LEVEL=info \
	FORGE_ENV=development

ENTRYPOINT ["java", "-jar", "/app/demo-kotlin-api.jar"]
