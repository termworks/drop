plugins {
    id("com.android.application")
    id("org.jetbrains.kotlin.android")
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
        versionCode = 1
        versionName = project.findProperty("dropVersion")?.toString() ?: "0.0.0"
    }

    // The Go core is an AAR built by `make aar`, not a dependency anybody downloads.
    repositories {
        flatDir { dirs("${rootDir}/libs") }
    }

    sourceSets {
        getByName("main") {
            kotlin.srcDirs("src/main/kotlin")
        }
    }

    buildTypes {
        release {
            isMinifyEnabled = false
            // Signed with the debug key unless a release key is given, so CI produces something
            // installable rather than an unsigned artifact nobody can use.
            signingConfig = signingConfigs.getByName("debug")
        }
    }

    compileOptions {
        sourceCompatibility = JavaVersion.VERSION_17
        targetCompatibility = JavaVersion.VERSION_17
    }

    kotlinOptions {
        jvmTarget = "17"
    }

    // The AAR carries one .so per architecture and they are large; nothing here is architecture
    // specific beyond it.
    splits {
        abi {
            isEnable = false
        }
    }
}

dependencies {
    implementation(files("${rootDir}/libs/mobile.aar"))
    implementation("androidx.core:core-ktx:1.13.1")
}
