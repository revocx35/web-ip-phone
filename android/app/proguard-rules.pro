# Keep kotlinx.serialization generated serializers for the API models.
-keepattributes *Annotation*, InnerClasses
-dontnote kotlinx.serialization.**
-keepclassmembers @kotlinx.serialization.Serializable class ** {
    *** Companion;
    kotlinx.serialization.KSerializer serializer(...);
}
-keep,includedescriptorclasses class io.github.revocx35.webipphone.**$$serializer { *; }
-keepclassmembers class io.github.revocx35.webipphone.** {
    *** Companion;
}
# Readable stack traces in crash reports; the code is open source anyway.
-dontobfuscate
