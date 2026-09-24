# gomobile's generated classes are looked up from native code by name, so nothing in them may be
# renamed or removed.
-keep class go.** { *; }
-keep class mobile.** { *; }
-keep interface mobile.** { *; }

# The Kotlin that implements the Go callbacks is reached the same way.
-keep class * implements mobile.Events { *; }
-keep class * implements mobile.Screen { *; }
-keep class * implements mobile.Hardware { *; }

# YubiKit names a findbugs annotation it does not ship; it is only ever read by a linter.
-dontwarn edu.umd.cs.findbugs.annotations.**
# And its FIDO session is reached through the connection types it looks up by class.
-keep class com.yubico.yubikit.** { *; }
