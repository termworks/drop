# gomobile's generated classes are looked up from native code by name, so nothing in them may be
# renamed or removed.
-keep class go.** { *; }
-keep class mobile.** { *; }
-keep interface mobile.** { *; }

# The Kotlin that implements the Go callbacks is reached the same way.
-keep class * implements mobile.Events { *; }
-keep class * implements mobile.Screen { *; }
