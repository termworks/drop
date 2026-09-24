package dev.bresilla.drop

import com.yubico.yubikit.core.YubiKeyDevice
import com.yubico.yubikit.core.fido.FidoConnection
import com.yubico.yubikit.core.smartcard.SmartCardConnection
import com.yubico.yubikit.fido.ctap.ClientPin
import com.yubico.yubikit.fido.ctap.CredentialManagement
import com.yubico.yubikit.fido.ctap.Ctap2Session
import com.yubico.yubikit.fido.ctap.PinUvAuthProtocolV2
import java.util.concurrent.CompletableFuture
import java.util.concurrent.ExecutionException
import java.util.concurrent.Executors
import java.util.concurrent.TimeUnit
import java.util.concurrent.TimeoutException
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import mobile.Hardware

/**
 * The user key in a YubiKey, on the phone: the Go core asks for one FIDO assertion to sign with, and
 * this puts up a prompt and waits while somebody holds the key to the phone or plugs it in. It also
 * reads which key the YubiKey holds, for a phone that is to become that key.
 *
 * Only the assertion and the reading happen here. What is signed, and the OpenSSH signature made of
 * it, are the core's, so a badge the phone signs is the same one ssh-keygen would have made.
 */
object YubiKey : Hardware {
    /** One signature waiting for a YubiKey. */
    class Ask(val application: String, val handle: ByteArray, val hash: ByteArray) {
        val result = CompletableFuture<ByteArray>()
    }

    /** A key read off a YubiKey: which application it is under, its ed25519 key, and its handle. */
    class Credential(val application: String, val key: ByteArray, val handle: ByteArray)

    /** Reading which key a YubiKey holds, unlocked by its PIN. */
    class Read(val pin: CharArray) {
        val result = CompletableFuture<Credential>()
    }

    private val _asking = MutableStateFlow<Ask?>(null)
    val asking: StateFlow<Ask?> = _asking

    private val _reading = MutableStateFlow<Read?>(null)
    val reading: StateFlow<Read?> = _reading

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

    /** Waits for the YubiKey to be held to the phone, and reads the key for drop off it. */
    fun read(pin: CharArray): Credential {
        val read = Read(pin)
        _reading.value = read
        try {
            return read.result.get(90, TimeUnit.SECONDS)
        } catch (e: TimeoutException) {
            throw Exception("nobody held the YubiKey to the phone in time")
        } catch (e: ExecutionException) {
            throw Exception(e.cause?.message ?: "the YubiKey could not be read")
        } finally {
            read.pin.fill(' ')
            if (_reading.value === read) _reading.value = null
        }
    }

    /** Gives up on what is waiting, when whoever was asked to hold the key said no. */
    fun cancel() {
        _reading.value?.result?.completeExceptionally(Exception("the YubiKey was not held to the phone"))
        _asking.value?.result?.completeExceptionally(Exception("the YubiKey was not held to the phone"))
    }

    /** Has the YubiKey that just turned up do what is waiting, off the thread that found it. */
    fun found(device: YubiKeyDevice) {
        val read = _reading.value
        val ask = _asking.value
        if (read == null && ask == null) return
        work.execute {
            try {
                val session = { use: (Ctap2Session) -> Unit ->
                    if (device.supportsConnection(FidoConnection::class.java)) {
                        device.openConnection(FidoConnection::class.java).use { use(Ctap2Session(it)) }
                    } else {
                        device.openConnection(SmartCardConnection::class.java).use { use(Ctap2Session(it)) }
                    }
                }
                if (read != null) {
                    session { read.result.complete(credential(it, read.pin)) }
                } else if (ask != null) {
                    session { ask.result.complete(assertion(it, ask)) }
                }
            } catch (e: Exception) {
                read?.result?.completeExceptionally(e)
                ask?.result?.completeExceptionally(e)
            }
        }
    }

    private fun assertion(session: Ctap2Session, ask: Ask): ByteArray {
        val allow = listOf(mapOf("type" to "public-key", "id" to ask.handle))
        val made = session.getAssertions(ask.application, ask.hash, allow, null, null, null, null, null).first()
        return made.authenticatorData + made.signature
    }

    /** The key for drop a YubiKey holds: the one made for drop, else its SSH key, listed with its PIN. */
    private fun credential(session: Ctap2Session, pin: CharArray): Credential {
        if (!CredentialManagement.isSupported(session.info)) throw Exception("this YubiKey cannot say which keys it holds")
        val protocol = PinUvAuthProtocolV2()
        val token = ClientPin(session, protocol).getPinToken(pin, ClientPin.PIN_PERMISSION_CM, null)
        val listed = CredentialManagement(session, protocol, token)
        val ssh = listed.enumerateRps().filter { (it.rp["id"] as? String)?.startsWith("ssh:") == true }
        val rp = ssh.firstOrNull { it.rp["id"] == "ssh:drop" } ?: ssh.firstOrNull()
            ?: throw Exception("this YubiKey holds no SSH key yet: on a computer, drop me key yubikey --new makes one")
        for (held in listed.enumerateCredentials(rp.rpIdHash)) {
            val cose = held.publicKey as Map<*, *>
            val alg = cose[3] ?: cose["3"]
            val x = (cose[-2] ?: cose["-2"]) as? ByteArray
            val id = (held.credentialId as Map<*, *>)["id"] as? ByteArray
            if ((alg == -8 || alg == -8L) && x != null && id != null) return Credential(rp.rp["id"] as String, x, id)
        }
        throw Exception("the SSH key on this YubiKey is not an ed25519 one, which is the kind drop signs with")
    }
}
