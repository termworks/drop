plugins {
    id("com.android.application")
    id("org.jetbrains.kotlin.android")
    id("org.jetbrains.kotlin.plugin.compose")
}

val dropVersion = project.findProperty("dropVersion")?.toString() ?: "0.0.0"

// A version as the number Android compares: each release must be a larger one to install over the
// last, so 0.4.3 is 403 and 1.2.0 is 10200.
fun codeOf(version: String): Int {
    val parts = version.split(".").map { part -> part.takeWhile { it.isDigit() }.toIntOrNull() ?: 0 } + listOf(0, 0, 0)
    return maxOf(1, parts[0] * 10000 + parts[1] * 100 + parts[2])
}

// The key every release is signed with, when one is given. The same key on every build is what lets
// an installed app take the next one as an update; without one it is this machine's debug key.
val releaseKey: String? = System.getenv("DROP_KEYSTORE")?.takeIf { it.isNotBlank() && file(it).exists() }

android {
    namespace = "dev.bresilla.drop"
    compileSdk = 35

    // The SDK comes from the flake and carries exactly one of these. Left unset, the plugin asks
    // for whichever version it was built against and fails on a store path it cannot add to.
    buildToolsVersion = "35.0.0"

    defaultConfig {
        applicationId = "dev.bresilla.drop"
        minSdk = 26
        targetSdk = 35
        versionCode = codeOf(dropVersion)
        versionName = dropVersion
    }

    signingConfigs {
        if (releaseKey != null) {
            create("release") {
                storeFile = file(releaseKey)
                storePassword = System.getenv("DROP_KEYSTORE_PASSWORD")
                keyAlias = System.getenv("DROP_KEY_ALIAS") ?: "drop"
                keyPassword = System.getenv("DROP_KEY_PASSWORD") ?: System.getenv("DROP_KEYSTORE_PASSWORD")
            }
        }
    }

    sourceSets {
        getByName("main") {
            kotlin.srcDirs("src/main/kotlin")
        }
    }

    buildTypes {
        release {
            // Shrunk, because the icon set alone is thousands of classes and the app uses twenty.
            // The Go side is reached from native code by name, which is what the rules keep.
            isMinifyEnabled = true
            isShrinkResources = true
            proguardFiles(getDefaultProguardFile("proguard-android-optimize.txt"), "proguard-rules.pro")
            signingConfig = signingConfigs.getByName(if (releaseKey != null) "release" else "debug")
        }
    }

    buildFeatures {
        compose = true
    }

    // The Go core is most of the app, one copy per architecture. Stored, which is the default, it is
    // mapped straight from the APK and costs its whole size in the download; compressed it is
    // unpacked once at install and the APK is a third of the size.
    packaging {
        jniLibs {
            useLegacyPackaging = true
        }
    }

    compileOptions {
        sourceCompatibility = JavaVersion.VERSION_17
        targetCompatibility = JavaVersion.VERSION_17
    }

    kotlinOptions {
        jvmTarget = "17"
    }
}

dependencies {
    // The Go core, built by `make aar` rather than downloaded.
    implementation(files("${rootDir}/libs/mobile.aar"))

    implementation(platform("androidx.compose:compose-bom:2024.12.01"))
    implementation("androidx.compose.ui:ui")
    implementation("androidx.compose.material3:material3")
    implementation("androidx.compose.material:material-icons-extended")
    implementation("androidx.activity:activity-compose:1.9.3")
    implementation("androidx.core:core-ktx:1.13.1")

    // Reading a pairing code off another screen.
    implementation("com.journeyapps:zxing-android-embedded:4.3.0")
}
