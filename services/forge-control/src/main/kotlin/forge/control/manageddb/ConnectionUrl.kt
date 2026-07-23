package forge.control.manageddb

import java.net.URLEncoder
import java.nio.charset.StandardCharsets

/**
 * Compose a PostgreSQL connection URL from discrete parts.
 * Password and username are percent-encoded; the URL must never be logged.
 */
object ConnectionUrl {
    fun compose(
        username: String,
        password: String,
        host: String,
        port: Int,
        database: String,
    ): String {
        require(username.isNotBlank()) { "username must not be blank" }
        require(host.isNotBlank()) { "host must not be blank" }
        require(database.isNotBlank()) { "database must not be blank" }
        require(port in 1..65535) { "port must be 1–65535" }
        val user = enc(username)
        val pass = enc(password)
        return "postgresql://$user:$pass@$host:$port/$database"
    }

    private fun enc(value: String): String =
        URLEncoder.encode(value, StandardCharsets.UTF_8).replace("+", "%20")
}
