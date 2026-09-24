plugins {
    id("com.android.application")
    id("org.jetbrains.kotlin.android")
    id("org.jetbrains.kotlin.plugin.compose")
}

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
        versionCode = 2
        versionName = project.findProperty("dropVersion")?.toString() ?: "0.0.0"
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
            // Signed with the debug key unless a release key is given, so CI produces something
            // installable rather than an unsigned artifact nobody can use.
            signingConfig = signingConfigs.getByName("debug")
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
