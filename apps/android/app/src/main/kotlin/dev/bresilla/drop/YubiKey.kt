package dev.bresilla.drop

import com.yubico.yubikit.core.YubiKeyDevice
import com.yubico.yubikit.core.fido.FidoConnection
import com.yubico.yubikit.core.smartcard.SmartCardConnection
import com.yubico.yubikit.fido.ctap.Ctap2Session
import java.util.concurrent.CompletableFuture
import java.util.concurrent.ExecutionException
import java.util.concurrent.Executors
import java.util.concurrent.TimeUnit
import java.util.concurrent.TimeoutException
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import mobile.Hardware

/**
 * The user key in a YubiKey, signing on the phone: the Go core asks for one FIDO assertion, and this
 * puts up a prompt and waits while somebody holds the key to the phone or plugs it in.
 *
 * Only the assertion happens here. What it is over, and the OpenSSH signature made of it, are the
 * core's, so a badge the phone signs is the same one ssh-keygen would have made on a computer.
 */
object YubiKey : Hardware {
    /** One signature waiting for a YubiKey. */
    class Ask(val application: String, val handle: ByteArray, val hash: ByteArray) {
        val result = CompletableFuture<ByteArray>()
    }

    private val _asking = MutableStateFlow<Ask?>(null)
    val asking: StateFlow<Ask?> = _asking

    private val work = Executors.newSingleThreadExecutor()

    override fun sign(application: String, handle: ByteArray, clientDataHash: ByteArray): ByteArray {
        val ask = Ask(application, handle, clientDataHash)
        _asking.value = ask
        try {
            return ask.result.get(90, TimeUnit.SECONDS)
        } catch (e: TimeoutException) {
            throw Exception("nobody held the YubiKey to the phone in time")
        } catch (e: ExecutionException) {
            throw Exception(e.cause?.message ?: "the YubiKey could not sign")
        } finally {
            if (_asking.value === ask) _asking.value = null
        }
    }

    /** Gives up on what is waiting, when whoever was asked to hold the key said no. */
    fun cancel() {
        _asking.value?.result?.completeExceptionally(Exception("the YubiKey was not held to the phone"))
    }

    /** Has the YubiKey that just turned up make the assertion waiting, off the thread that found it. */
    fun found(device: YubiKeyDevice) {
        val ask = _asking.value ?: return
        work.execute {
            try {
                val out = if (device.supportsConnection(FidoConnection::class.java)) {
                    device.openConnection(FidoConnection::class.java).use { assertion(Ctap2Session(it), ask) }
                } else {
                    device.openConnection(SmartCardConnection::class.java).use { assertion(Ctap2Session(it), ask) }
                }
                ask.result.complete(out)
            } catch (e: Exception) {
                ask.result.completeExceptionally(e)
            }
        }
    }

    private fun assertion(session: Ctap2Session, ask: Ask): ByteArray {
        val allow = listOf(mapOf("type" to "public-key", "id" to ask.handle))
        val made = session.getAssertions(ask.application, ask.hash, allow, null, null, null, null, null).first()
        return made.authenticatorData + made.signature
    }
}
