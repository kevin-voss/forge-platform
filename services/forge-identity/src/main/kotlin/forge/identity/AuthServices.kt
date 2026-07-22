package forge.identity

import forge.identity.auth.AuthService
import forge.identity.auth.CredentialStore
import forge.identity.auth.PasswordHasher
import forge.identity.auth.SessionStore
import forge.identity.config.AuthConfig
import forge.identity.config.TokenConfig
import forge.identity.db.Database
import forge.identity.logging.JsonLog
import forge.identity.token.TokenIntrospector
import javax.sql.DataSource

/** Wired auth stores + service for Identity HTTP routes. */
data class AuthServices(
    val credentials: CredentialStore,
    val sessions: SessionStore,
    val hasher: PasswordHasher,
    val auth: AuthService,
) {
    companion object {
        fun from(
            dataSource: DataSource,
            tenancy: TenancyServices,
            authConfig: AuthConfig,
            tokenConfig: TokenConfig = TokenConfig(),
            tokenIntrospector: TokenIntrospector? = null,
            log: JsonLog? = null,
        ): AuthServices {
            val hasher = PasswordHasher(
                memoryKb = authConfig.argon2MemoryKb,
                iterations = authConfig.argon2Iterations,
                parallelism = authConfig.argon2Parallelism,
            )
            val credentials = CredentialStore(dataSource)
            val sessions = SessionStore(
                dataSource = dataSource,
                sessionTtlSeconds = authConfig.sessionTtlSeconds,
            )
            val introspector = tokenIntrospector
                ?: TokenServices.from(dataSource, tenancy, tokenConfig, log).introspector
            val auth = AuthService(
                dataSource = dataSource,
                users = tenancy.users,
                credentials = credentials,
                sessions = sessions,
                hasher = hasher,
                authConfig = authConfig,
                tokenIntrospector = introspector,
                log = log,
            )
            return AuthServices(
                credentials = credentials,
                sessions = sessions,
                hasher = hasher,
                auth = auth,
            )
        }

        fun from(
            db: Database,
            tenancy: TenancyServices,
            authConfig: AuthConfig,
            tokenConfig: TokenConfig = TokenConfig(),
            tokenIntrospector: TokenIntrospector? = null,
            log: JsonLog? = null,
        ): AuthServices = from(
            dataSource = db.dataSource,
            tenancy = tenancy,
            authConfig = authConfig,
            tokenConfig = tokenConfig,
            tokenIntrospector = tokenIntrospector,
            log = log,
        )
    }
}
