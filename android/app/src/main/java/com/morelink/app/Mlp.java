package com.morelink.app;

import android.util.Log;

import java.io.ByteArrayOutputStream;
import java.io.DataInputStream;
import java.io.DataOutputStream;
import java.io.IOException;
import java.io.InputStream;
import java.io.OutputStream;
import java.nio.charset.StandardCharsets;
import java.security.MessageDigest;
import java.security.SecureRandom;
import java.util.Arrays;

import javax.crypto.Cipher;
import javax.crypto.spec.GCMParameterSpec;
import javax.crypto.spec.SecretKeySpec;

/**
 * MoreLink Protocol v1 Java implementation. Wire format is identical to the
 * Go reference in internal/mlp so the two apps interoperate.
 */
public final class Mlp {
    public static final int MAX_FRAME = 8 << 20;

    public static final byte TYPE_HELLO = 0x01;
    public static final byte TYPE_PAIR_REQ = 0x02;
    public static final byte TYPE_PAIR_RESULT = 0x03;
    public static final byte TYPE_CLIP_TEXT = 0x10;
    public static final byte TYPE_CLIP_IMAGE = 0x11;
    public static final byte TYPE_NOTIFY = 0x20;
    public static final byte TYPE_FILE_OFFER = 0x30;
    public static final byte TYPE_FILE_CHUNK = 0x31;
    public static final byte TYPE_FILE_ACK = 0x32;
    public static final byte TYPE_FILE_DONE = 0x33;
    public static final byte TYPE_PING = 0x40;
    public static final byte TYPE_PONG = 0x41;
    public static final byte TYPE_PEER_INFO = 0x50;

    public static final int DISCOVERY_PORT = 47891;
    public static final int DATA_PORT = 47892;
    public static final int RELAY_PORT = 47893;
    public static final int VERSION = 1;

    private static final SecureRandom RNG = new SecureRandom();
    private static final char[] HEX = "0123456789abcdef".toCharArray();

    private Mlp() {}

    /** SHA-256(PIN + ":" + min(idA,idB) + ":" + max(idA,idB)), order independent. */
    public static byte[] deriveKey(String pin, String idA, String idB) {
        String lo, hi;
        if (idA.compareTo(idB) <= 0) { lo = idA; hi = idB; }
        else { lo = idB; hi = idA; }
        try {
            MessageDigest md = MessageDigest.getInstance("SHA-256");
            return md.digest((pin + ":" + lo + ":" + hi).getBytes(StandardCharsets.UTF_8));
        } catch (Exception e) {
            throw new RuntimeException(e);
        }
    }

    /** GCM plaintext = type || payload, nonce random 12 bytes, tag 16 bytes appended. */
    private static byte[] seal(byte[] key, byte type, byte[] payload, byte[] nonceOut) {
        try {
            Cipher c = Cipher.getInstance("AES/GCM/NoPadding");
            SecretKeySpec k = new SecretKeySpec(key, "AES");
            byte[] nonce = new byte[12];
            RNG.nextBytes(nonce);
            if (nonceOut != null) System.arraycopy(nonce, 0, nonceOut, 0, 12);
            c.init(Cipher.ENCRYPT_MODE, k, new GCMParameterSpec(128, nonce));
            c.updateAAD(new byte[]{type});
            byte[] ct = c.doFinal(payload);
            ByteArrayOutputStream out = new ByteArrayOutputStream(1 + 12 + ct.length);
            out.write(type);
            out.write(nonce, 0, 12);
            out.write(ct, 0, ct.length);
            return out.toByteArray();
        } catch (Exception e) {
            throw new RuntimeException(e);
        }
    }

    private static byte[] open(byte[] key, byte[] body) throws IOException {
        if (body.length < 13) throw new IOException("short encrypted frame");
        byte type = body[0];
        try {
            Cipher c = Cipher.getInstance("AES/GCM/NoPadding");
            SecretKeySpec k = new SecretKeySpec(key, "AES");
            byte[] nonce = Arrays.copyOfRange(body, 1, 13);
            byte[] ct = Arrays.copyOfRange(body, 13, body.length);
            c.init(Cipher.DECRYPT_MODE, k, new GCMParameterSpec(128, nonce));
            c.updateAAD(new byte[]{type});
            byte[] plain = c.doFinal(ct);
            ByteArrayOutputStream out = new ByteArrayOutputStream(1 + plain.length);
            out.write(type);
            out.write(plain, 0, plain.length);
            return out.toByteArray();
        } catch (Exception e) {
            throw new IOException("decrypt failed", e);
        }
    }

    public static void writeFrame(OutputStream os, byte type, byte[] payload, byte[] key) throws IOException {
        byte[] body;
        if (key == null) {
            body = new byte[1 + payload.length];
            body[0] = type;
            System.arraycopy(payload, 0, body, 1, payload.length);
        } else {
            body = seal(key, type, payload, null);
        }
        if (body.length > MAX_FRAME) throw new IOException("frame too large");
        DataOutputStream dos = new DataOutputStream(os);
        dos.writeInt(body.length);
        dos.write(body);
        dos.flush();
    }

    /** Returns {type, payload}, or null on EOF. */
    public static byte[] readFrame(InputStream is, byte[] key) throws IOException {
        DataInputStream dis = new DataInputStream(is);
        int len;
        try {
            len = dis.readInt();
        } catch (java.io.EOFException e) {
            return null;
        }
        if (len < 1 || len > MAX_FRAME) throw new IOException("invalid frame length " + len);
        byte[] body = new byte[len];
        dis.readFully(body);
        if (key == null) {
            byte[] payload = Arrays.copyOfRange(body, 1, body.length);
            byte[] result = new byte[1 + payload.length];
            result[0] = body[0];
            System.arraycopy(payload, 0, result, 1, payload.length);
            return result;
        }
        return open(key, body);
    }

    public static byte frameType(byte[] frame) { return frame[0]; }
    public static byte[] framePayload(byte[] frame) { return Arrays.copyOfRange(frame, 1, frame.length); }

    public static byte[] utf8(String s) { return s.getBytes(StandardCharsets.UTF_8); }
    public static String str(byte[] b) { return new String(b, StandardCharsets.UTF_8); }

    public static String makeId() {
        byte[] b = new byte[8];
        RNG.nextBytes(b);
        return hex(b);
    }

    public static String makePin() {
        int n = RNG.nextInt(1000000);
        return String.format("%06d", n);
    }

    public static String hex(byte[] b) {
        char[] out = new char[b.length * 2];
        for (int i = 0; i < b.length; i++) {
            out[i * 2] = HEX[(b[i] >> 4) & 0xF];
            out[i * 2 + 1] = HEX[b[i] & 0xF];
        }
        return new String(out);
    }

    /** Minimal JSON string-field extraction without external libs. */
    public static String jsonString(String json, String key) {
        String pattern = "\"" + key + "\"";
        int i = json.indexOf(pattern);
        if (i < 0) return null;
        i = json.indexOf(':', i + pattern.length());
        if (i < 0) return null;
        i++;
        while (i < json.length() && Character.isWhitespace(json.charAt(i))) i++;
        if (i >= json.length() || json.charAt(i) != '"') return null;
        i++;
        StringBuilder sb = new StringBuilder();
        while (i < json.length()) {
            char c = json.charAt(i++);
            if (c == '"') break;
            if (c == '\\' && i < json.length()) {
                char n = json.charAt(i++);
                switch (n) {
                    case 'n': sb.append('\n'); break;
                    case 'r': sb.append('\r'); break;
                    case 't': sb.append('\t'); break;
                    case '"': sb.append('"'); break;
                    case '\\': sb.append('\\'); break;
                    default: sb.append(n);
                }
            } else sb.append(c);
        }
        return sb.toString();
    }

    public static String jsonEscape(String s) {
        if (s == null) s = "";
        return s.replace("\\", "\\\\").replace("\"", "\\\"")
                .replace("\n", "\\n").replace("\r", "\\r").replace("\t", "\\t");
    }

    public static String helloJson(String id, String name, String platform, int tcp) {
        return "{\"v\":" + VERSION + ",\"id\":\"" + jsonEscape(id) +
                "\",\"name\":\"" + jsonEscape(name) +
                "\",\"platform\":\"" + platform + "\",\"tcp\":" + tcp + "}";
    }
}
