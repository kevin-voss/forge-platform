package forge.control.resource

/** Scope at which a kind's names are unique. */
enum class ResourceScope {
    /** Unique per (organization, name); project and environment are null. */
    Cluster,

    /** Unique per (organization, project, name); environment is null. */
    Project,

    /** Unique per (organization, project, environment, name). */
    Environment,
}
