package forge.identity

import forge.identity.db.Database
import forge.identity.logging.JsonLog
import forge.identity.org.OrgStore
import forge.identity.project.ProjectMembershipStore
import forge.identity.user.UserStore
import javax.sql.DataSource

/** Wired tenancy stores for Identity HTTP routes. */
data class TenancyServices(
    val users: UserStore,
    val orgs: OrgStore,
    val projects: ProjectMembershipStore,
) {
    companion object {
        fun from(dataSource: DataSource, log: JsonLog? = null): TenancyServices {
            val users = UserStore(dataSource, log)
            val orgs = OrgStore(dataSource, users, log)
            val projects = ProjectMembershipStore(dataSource, users, orgs, log)
            return TenancyServices(users = users, orgs = orgs, projects = projects)
        }

        fun from(db: Database, log: JsonLog? = null): TenancyServices =
            from(db.dataSource, log)
    }
}
