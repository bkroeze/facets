package facets.mobile.settings

import facets.mobile.data.transport.InvalidServerUrlException
import facets.mobile.data.transport.ServerUrlValidator
import org.junit.Assert.assertEquals
import org.junit.Assert.assertThrows
import org.junit.Test

class ServerUrlValidationTest {
    @Test
    fun canonicalizesHttpsOrigin() {
        assertEquals(
            "https://facets.example.ts.net",
            ServerUrlValidator.canonicalize("  https://facets.example.ts.net/  "),
        )
    }

    @Test
    fun rejectsCleartextCredentialsAndOriginDecorations() {
        listOf(
            "http://facets.example.ts.net",
            "https://user:secret@facets.example.ts.net",
            "https://facets.example.ts.net/api",
            "https://facets.example.ts.net?token=secret",
            "https://facets.example.ts.net/#health",
            "not a URL",
        ).forEach { input ->
            assertThrows(InvalidServerUrlException::class.java) {
                ServerUrlValidator.validate(input)
            }
        }
    }
}
