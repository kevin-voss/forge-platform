package forge.identity

import forge.identity.config.TokenConfig
import forge.identity.db.Database
import forge.identity.logging.JsonLog
import forge.identity.token.ServiceAccountStore
import forge.identity.token.TokenIntrospector
import forge.identity.token.TokenStore
import javax.sql.DataSource

/** Wired API token + service-account stores for Identity HTTP routes. */
data class TokenServices(
    val serviceAccounts: ServiceAccountStore,
    val tokens: TokenStore,
    val introspector: TokenIntrospector,
) {
    companion object {
        fun from(
            dataSource: DataSource,
            tenancy: TenancyServices,
            tokenConfig: TokenConfig = TokenConfig(),
            log: JsonLog? = null,
        ): TokenServices {
            val serviceAccounts = ServiceAccountStore(
                dataSource = dataSource,
                projects = tenancy.projects,
                log = log,
            )
            val tokens = TokenStore(
                dataSource = dataSource,
                tokenConfig = tokenConfig,
                projects = tenancy.projects,
                users = tenancy.users,
                orgs = tenancy.orgs,
                serviceAccounts = serviceAccounts,
                log = log,
            )
            return TokenServices(
                serviceAccounts = serviceAccounts,
                tokens = tokens,
                introspector = TokenIntrospector(tokens),
            )
        }

        fun from(
            db: Database,
            tenancy: TenancyServices,
            tokenConfig: TokenConfig = TokenConfig(),
            log: JsonLog? = null,
        ): TokenServices = from(db.dataSource, tenancy, tokenConfig, log)
    }
}
