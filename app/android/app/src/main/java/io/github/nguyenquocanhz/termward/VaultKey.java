package io.github.nguyenquocanhz.termward;

import android.content.Context;
import android.content.SharedPreferences;
import android.security.keystore.KeyGenParameterSpec;
import android.security.keystore.KeyProperties;
import android.util.Base64;

import java.security.KeyStore;
import java.security.SecureRandom;

import javax.crypto.Cipher;
import javax.crypto.KeyGenerator;
import javax.crypto.SecretKey;
import javax.crypto.spec.GCMParameterSpec;

/**
 * The 32-byte key that encrypts remembered passwords (core/internal/secret).
 * It is stored wrapped by an AES key that never leaves the Android Keystore.
 */
final class VaultKey {
    private static final String ALIAS = "termward-vault";
    private static final String PREF = "vault";

    private VaultKey() {}

    static String get(Context ctx) throws Exception {
        SharedPreferences prefs = ctx.getSharedPreferences("termward", Context.MODE_PRIVATE);
        SecretKey wrap = wrappingKey();
        String stored = prefs.getString(PREF, null);
        if (stored != null) {
            try {
                String[] parts = stored.split(":");
                Cipher c = Cipher.getInstance("AES/GCM/NoPadding");
                c.init(Cipher.DECRYPT_MODE, wrap, new GCMParameterSpec(128, Base64.decode(parts[0], Base64.NO_WRAP)));
                return hex(c.doFinal(Base64.decode(parts[1], Base64.NO_WRAP)));
            } catch (Exception ignored) {
                // Unreadable (keystore reset): fall through and create a new one.
            }
        }
        byte[] raw = new byte[32];
        new SecureRandom().nextBytes(raw);
        Cipher c = Cipher.getInstance("AES/GCM/NoPadding");
        c.init(Cipher.ENCRYPT_MODE, wrap);
        String iv = Base64.encodeToString(c.getIV(), Base64.NO_WRAP);
        String ct = Base64.encodeToString(c.doFinal(raw), Base64.NO_WRAP);
        prefs.edit().putString(PREF, iv + ":" + ct).apply();
        return hex(raw);
    }

    private static SecretKey wrappingKey() throws Exception {
        KeyStore ks = KeyStore.getInstance("AndroidKeyStore");
        ks.load(null);
        if (!ks.containsAlias(ALIAS)) {
            KeyGenerator kg = KeyGenerator.getInstance(KeyProperties.KEY_ALGORITHM_AES, "AndroidKeyStore");
            kg.init(new KeyGenParameterSpec.Builder(ALIAS,
                    KeyProperties.PURPOSE_ENCRYPT | KeyProperties.PURPOSE_DECRYPT)
                    .setBlockModes(KeyProperties.BLOCK_MODE_GCM)
                    .setEncryptionPaddings(KeyProperties.ENCRYPTION_PADDING_NONE)
                    .setKeySize(256)
                    .build());
            kg.generateKey();
        }
        return ((KeyStore.SecretKeyEntry) ks.getEntry(ALIAS, null)).getSecretKey();
    }

    private static String hex(byte[] b) {
        StringBuilder sb = new StringBuilder(b.length * 2);
        for (byte x : b) sb.append(String.format("%02x", x));
        return sb.toString();
    }
}
