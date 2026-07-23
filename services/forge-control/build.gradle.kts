plugins {
    kotlin("jvm") version "2.1.21"
    kotlin("plugin.serialization") version "2.1.21"
    application
}

group = "forge.control"
version = "0.1.0"

repositories {
    mavenCentral()
}

val ktorVersion = "3.1.3"
val flywayVersion = "11.3.1"
val otelVersion = "1.46.0"

dependencies {
    implementation("io.ktor:ktor-server-core:$ktorVersion")
    implementation("io.ktor:ktor-server-netty:$ktorVersion")
    implementation("io.ktor:ktor-server-content-negotiation:$ktorVersion")
    implementation("io.ktor:ktor-server-call-logging:$ktorVersion")
    implementation("io.ktor:ktor-server-status-pages:$ktorVersion")
    implementation("io.ktor:ktor-serialization-kotlinx-json:$ktorVersion")
    implementation("org.jetbrains.kotlinx:kotlinx-serialization-json:1.8.1")
    // Keep framework SLF4J quiet; application logs are structured JSON via JsonLog.
    implementation("org.slf4j:slf4j-nop:2.0.17")
    implementation("io.opentelemetry:opentelemetry-api:$otelVersion")
    implementation("io.opentelemetry:opentelemetry-sdk:$otelVersion")
    implementation("io.opentelemetry:opentelemetry-exporter-otlp:$otelVersion")

    implementation("org.postgresql:postgresql:42.7.5")
    implementation("com.zaxxer:HikariCP:6.2.1")
    implementation("org.flywaydb:flyway-core:$flywayVersion")
    implementation("org.flywaydb:flyway-database-postgresql:$flywayVersion")

    testImplementation(kotlin("test"))
    testImplementation("io.ktor:ktor-server-test-host:$ktorVersion")
    testImplementation("io.ktor:ktor-client-content-negotiation:$ktorVersion")
    testImplementation("org.junit.jupiter:junit-jupiter:5.12.2")
}

kotlin {
    jvmToolchain(21)
}

application {
    mainClass.set("forge.control.ApplicationKt")
}

tasks.test {
    useJUnitPlatform()
    // Integration tests need Docker/Postgres; excluded from default + Docker image build.
    exclude("**/*IntegrationTest*")
}

tasks.register<Test>("integrationTest") {
    description = "Runs repository/migration integration tests (foundation Postgres)."
    group = "verification"
    useJUnitPlatform()
    testClassesDirs = sourceSets["test"].output.classesDirs
    classpath = sourceSets["test"].runtimeClasspath
    include("**/*IntegrationTest*")
    shouldRunAfter(tasks.test)
}

tasks.register<JavaExec>("migrate") {
    description = "Apply Flyway migrations without starting the HTTP server."
    group = "application"
    classpath = sourceSets["main"].runtimeClasspath
    mainClass.set("forge.control.MigrateKt")
}

// Fat-jar zipTree + EXCLUDE drops duplicate META-INF/services files, which breaks
// Flyway's ServiceLoader plugins (CoreResourceTypeProvider, filesystem/classpath
// scanners). Unrecognized SQL names are then silently skipped → 0 migrations.
val mergeFlywayPlugins = tasks.register("mergeFlywayPlugins") {
    val outFile = layout.buildDirectory.file(
        "merged-services/org.flywaydb.core.extensibility.Plugin",
    )
    outputs.file(outFile)
    inputs.files(configurations.runtimeClasspath)
    doLast {
        val lines = linkedSetOf<String>()
        configurations.runtimeClasspath.get().forEach { file ->
            if (!file.isFile || file.extension != "jar") return@forEach
            zipTree(file)
                .matching { include("META-INF/services/org.flywaydb.core.extensibility.Plugin") }
                .files
                .forEach { service ->
                    service.readLines()
                        .map { it.trim() }
                        .filter { it.isNotEmpty() && !it.startsWith("#") }
                        .forEach { lines.add(it) }
                }
        }
        val target = outFile.get().asFile
        target.parentFile.mkdirs()
        target.writeText(lines.joinToString("\n", postfix = "\n"))
    }
}

tasks.jar {
    dependsOn(mergeFlywayPlugins)
    manifest {
        attributes["Main-Class"] = "forge.control.ApplicationKt"
    }
    duplicatesStrategy = DuplicatesStrategy.EXCLUDE
    from(configurations.runtimeClasspath.get().map { if (it.isDirectory) it else zipTree(it) })
    // Replace the clobbered SPI descriptor with the merged Flyway plugin list.
    exclude("META-INF/services/org.flywaydb.core.extensibility.Plugin")
    from(layout.buildDirectory.dir("merged-services")) {
        into("META-INF/services")
    }
}
