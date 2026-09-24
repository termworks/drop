package dev.bresilla.drop.ui

import android.net.Uri
import androidx.activity.compose.BackHandler
import androidx.compose.animation.AnimatedContent
import androidx.compose.animation.core.tween
import androidx.compose.animation.fadeIn
import androidx.compose.animation.fadeOut
import androidx.compose.animation.slideInHorizontally
import androidx.compose.animation.slideOutHorizontally
import androidx.compose.animation.togetherWith
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import dev.bresilla.drop.Drop

/** Where somebody is. Entering rather than tabbing: what a path is depends on the machine it is on. */
sealed interface Screen {
    data object Home : Screen
    data class Person(val name: String) : Screen
    data class Machine(val name: String) : Screen
    data class Chat(val machine: String) : Screen
    /** A files namespace: on another machine, or with local set, this phone's own copy kept in that directory. */
    data class Files(
        val machine: String,
        val path: String,
        val dir: String,
        val writable: Boolean,
        val shared: String = "",
        val local: String = "",
    ) : Screen
    data class Live(val machine: String, val path: String, val archetype: String, val typing: Boolean) : Screen
    data class Pair(val ticket: String? = null, val scan: Boolean = false) : Screen
    data class Sending(val uris: List<Uri>, val text: String? = null) : Screen
    /** A note: on another machine, or with machine empty, the copy this phone keeps at path. */
    data class Note(val machine: String, val path: String, val shared: String = "") : Screen
    /** Who may open a path: on this phone when machine is empty, or on a machine of yours. */
    data class Access(val machine: String, val path: String) : Screen
    data class AddMachine(val code: String? = null) : Screen
    data object Settings : Screen
}

/** Something the activity was handed from outside: a link, a notification, another app's share. */
sealed interface Arrival {
    data class Join(val ticket: String) : Arrival
    data class Open(val machine: String) : Arrival
    data class Send(val uris: List<Uri>, val text: String? = null) : Arrival
}

@Composable
fun App(arrival: Arrival?, taken: () -> Unit) {
    var stack by remember { mutableStateOf(listOf<Screen>(Screen.Home)) }
    var forward by remember { mutableStateOf(true) }

    val go: (Screen) -> Unit = { forward = true; stack = stack + it }
    val back: () -> Unit = { if (stack.size > 1) { forward = false; stack = stack.dropLast(1) } }
    // Replacing what is on top, for a screen that was only ever the way to another one.
    val instead: (Screen) -> Unit = { forward = true; stack = stack.dropLast(1) + it }
    val home: () -> Unit = { forward = false; stack = listOf(Screen.Home) }

    LaunchedEffect(arrival) {
        when (arrival) {
            is Arrival.Join -> stack = listOf(Screen.Home, if (Drop.machining(arrival.ticket)) Screen.AddMachine(arrival.ticket) else Screen.Pair(arrival.ticket))
            is Arrival.Open -> stack = listOf(Screen.Home, Screen.Chat(arrival.machine))
            is Arrival.Send -> stack = listOf(Screen.Home, Screen.Sending(arrival.uris, arrival.text))
            null -> return@LaunchedEffect
        }
        taken()
    }

    BackHandler(enabled = stack.size > 1, onBack = back)

    val top = stack.last()
    AnimatedContent(
        targetState = top,
        transitionSpec = {
            val shift = if (forward) 1 else -1
            (slideInHorizontally(tween(260)) { it * shift / 5 } + fadeIn(tween(260)))
                .togetherWith(slideOutHorizontally(tween(200)) { -it * shift / 5 } + fadeOut(tween(200)))
        },
        label = "screens",
    ) { screen ->
        when (screen) {
            Screen.Home -> PeopleScreen(go)
            is Screen.Person -> PersonScreen(screen.name, go, back, home)
            is Screen.Machine -> MachineScreen(screen.name, go, back, home)
            is Screen.Chat -> ChatScreen(screen.machine, go, back)
            is Screen.Files -> FilesScreen(screen, go, back)
            is Screen.Live -> LiveScreen(screen, back)
            is Screen.Pair -> PairScreen(screen.ticket, screen.scan, back, paired = { instead(Screen.Machine(it)) })
            is Screen.Sending -> SendingScreen(screen.uris, screen.text, back, done = { instead(Screen.Chat(it)) })
            is Screen.Note -> NoteScreen(screen, back)
            is Screen.Access -> AccessScreen(screen.machine, screen.path, back)
            is Screen.AddMachine -> AddMachineScreen(screen.code, back)
            Screen.Settings -> SettingsScreen(go, back)
        }
    }
}
