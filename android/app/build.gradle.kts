plugins {
    id("com.android.application")
}

android {
    namespace = "dev.bresilla.drop"
    compileSdk = 35

    defaultConfig {
        applicationId = "dev.bresilla.drop"
        minSdk = 26
        targetSdk = 35
        versionCode = 1
        versionName = "0.2.0"
    }

    buildTypes {
        // Signed with the debug key so the artifact installs straight from a release page. A store
        // upload would need a real key; sideloading does not.
        release {
            isMinifyEnabled = false
            signingConfig = signingConfigs.getByName("debug")
        }
    }

    compileOptions {
        sourceCompatibility = JavaVersion.VERSION_17
        targetCompatibility = JavaVersion.VERSION_17
    }
}

dependencies {
    // The whole app: drop's interface and its node, compiled by `gogio` from ./phone.
    //
    // Nothing else. The archive brings the support classes a Gio activity needs, and adding
    // appcompat beside it puts two copies of them on the classpath.
    implementation(files("../libs/drop.aar"))
}
