package com.morelink.app;

import android.app.Notification;
import android.app.NotificationChannel;
import android.app.NotificationManager;
import android.app.Service;
import android.content.ClipData;
import android.content.ClipboardManager;
import android.content.Context;
import android.content.Intent;
import android.content.SharedPreferences;
import android.os.Build;
import android.os.IBinder;
import android.util.Log;

import java.io.BufferedInputStream;
import java.io.DataOutputStream;
import java.io.File;
import java.io.FileOutputStream;
import java.io.IOException;
import java.io.InputStream;
import java.io.OutputStream;
import java.net.DatagramPacket;
import java.net.DatagramSocket;
import java.net.InetAddress;
import java.net.ServerSocket;
import java.net.Socket;
import java.nio.charset.StandardCharsets;
import java.security.MessageDigest;
import java.util.Arrays;
import java.util.concurrent.ExecutorService;
import java.util.concurrent.Executors;
import java.util.concurrent.atomic.AtomicBoolean;

/** Foreground service: LAN discovery, MLP TCP server, pairing and sync. */
public class LinkService extends Service {
    private static final String TAG = "MoreLink";
    public static final String PREFS = "morelink";
    public static final String ACTION_STATE = "com.morelink.STATE";
    public static final String EXTRA_STATE = "state";

    private final ExecutorService pool = Executors.newCachedThreadPool();
    private final AtomicBoolean running = new AtomicBoolean(false);
    private ServerSocket server;
    private DatagramSocket discovery;
    private final AtomicBoolean stopBeacon = new AtomicBoolean(false);

    // ---- Relay session (long-lived connection through the relay server) ----
    private static volatile Socket relaySocket;
    private static volatile OutputStream relayOut;
    private static volatile byte[] relayKey;
    private static final Object relayLock = new Object();
    private static final AtomicBoolean relayWanted = new AtomicBoolean(false);

    // State for an in-progress file received over the relay.
    private static class RelayRecv {
        String id;
        String name;
        long size;
        long got;
        java.io.FileOutputStream fos;
        java.security.MessageDigest digest;
    }
    private static volatile RelayRecv relayRecv;

    private String myId;
    private String myName;
    private byte[] key;
    private boolean paired;
    private String status = "就绪";
    private int notificationCount = 0;
    private int fileCount = 0;

    public static String stateJson(Context c) {
        SharedPreferences p = c.getSharedPreferences(PREFS, Context.MODE_PRIVATE);
        String id = p.getString("id", "");
        boolean paired = p.getBoolean("paired", false);
        String name = p.getString("name", "Android");
        return "{\"id\":\"" + Mlp.jsonEscape(id) + "\",\"name\":\"" + Mlp.jsonEscape(name) +
                "\",\"paired\":" + paired + ",\"pin\":\"" + Mlp.jsonEscape(p.getString("pin", "")) +
                "\",\"status\":\"" + Mlp.jsonEscape(statusStatic) + "\",\"notifications\":" + notificationCountStatic +
                ",\"files\":" + fileCountStatic + "}";
    }

    private static volatile String statusStatic = "就绪";
    private static volatile int notificationCountStatic = 0;
    private static volatile int fileCountStatic = 0;

    @Override
    public void onCreate() {
        super.onCreate();
        SharedPreferences p = getSharedPreferences(PREFS, MODE_PRIVATE);
        myId = p.getString("id", "");
        if (myId.isEmpty()) {
            myId = Mlp.makeId();
            p.edit().putString("id", myId).apply();
        }
        myName = p.getString("name", "Android");
        paired = p.getBoolean("paired", false);
        if (paired) {
            String pin = p.getString("pin", "");
            String peer = p.getString("peer", "");
            key = Mlp.deriveKey(pin, myId, peer);
        }
    }

    @Override
    public int onStartCommand(Intent intent, int flags, int startId) {
        startForeground(1, buildNotification("MoreLink 正在运行"));
        if (running.compareAndSet(false, true)) {
            pool.execute(this::discoveryLoop);
            pool.execute(this::serverLoop);
        }
        // Auto-start relay session if configured.
        SharedPreferences sp = getSharedPreferences(PREFS, MODE_PRIVATE);
        if (sp.getBoolean("relay_enabled", false)) {
            relayWanted.set(true);
            pool.execute(this::relayLoop);
        }
        return START_STICKY;
    }

    /** Long-lived session through the optional relay server. */
    private void relayLoop() {
        SharedPreferences p = getSharedPreferences(PREFS, MODE_PRIVATE);
        final String host = p.getString("relay_host", "");
        final int port = p.getInt("relay_port", Mlp.RELAY_PORT);
        final String room = p.getString("relay_room", "");
        final String pin = p.getString("relay_pin", "");
        if (host.isEmpty() || room.isEmpty() || pin.length() != 6) {
            statusStatic = "中继配置不完整";
            return;
        }
        final byte[] key = Mlp.deriveKey(pin, "", "");
        while (running.get() && relayWanted.get()) {
            Socket s = null;
            try {
                s = new Socket(host, port);
                s.setSoTimeout(0);
                OutputStream os = s.getOutputStream();
                InputStream is = new BufferedInputStream(s.getInputStream());
                // HELLO with room code; relay consumes this first frame.
                String helloJson = Mlp.helloJson(myId, myName, "android", Mlp.DATA_PORT);
                // inject room field
                helloJson = helloJson.substring(0, helloJson.length() - 1) + ",\"room\":\"" +
                        Mlp.jsonEscape(room) + "\"}";
                Mlp.writeFrame(os, Mlp.TYPE_HELLO, Mlp.utf8(helloJson), null);
                // Second HELLO travels transparently to the peer.
                Mlp.writeFrame(os, Mlp.TYPE_HELLO,
                        Mlp.utf8(Mlp.helloJson(myId, myName, "android", Mlp.DATA_PORT)), null);
                synchronized (relayLock) {
                    relaySocket = s;
                    relayOut = os;
                    relayKey = key;
                }
                paired = true;
                statusStatic = "已连接中继房间 " + room;
                sendBroadcast(new Intent(ACTION_STATE));
                Log.d(TAG, "relay connected: " + host + ":" + port + " room=" + room);

                // Serve incoming encrypted frames on this session.
                while (running.get() && relayWanted.get()) {
                    byte[] frame = Mlp.readFrame(is, key);
                    if (frame == null) break;
                    handleRelayFrame(frame, key);
                }
            } catch (Exception e) {
                Log.d(TAG, "relay loop: " + e.getMessage());
                statusStatic = "中继重连中…";
                sendBroadcast(new Intent(ACTION_STATE));
            } finally {
                synchronized (relayLock) {
                    if (relaySocket == s) {
                        relaySocket = null;
                        relayOut = null;
                        relayKey = null;
                    }
                }
                try { if (s != null) s.close(); } catch (Exception ignored) {}
            }
            try { Thread.sleep(3000); } catch (InterruptedException e) { break; }
        }
    }

    /** Process a frame received over the relay session. */
    private void handleRelayFrame(byte[] frame, byte[] key) {
        byte type = Mlp.frameType(frame);
        byte[] payload = Mlp.framePayload(frame);
        try {
            switch (type) {
                case Mlp.TYPE_PING:
                    relayWrite(Mlp.TYPE_PONG, new byte[0]);
                    break;
                case Mlp.TYPE_CLIP_TEXT:
                    ClipboardManager cm = (ClipboardManager) getSystemService(Context.CLIPBOARD_SERVICE);
                    if (cm != null) cm.setPrimaryClip(ClipData.newPlainText("MoreLink", Mlp.str(payload)));
                    notificationCountStatic++;
                    statusStatic = "已接收剪贴板文本";
                    sendBroadcast(new Intent(ACTION_STATE));
                    break;
                case Mlp.TYPE_FILE_OFFER:
                    startRelayFileReceive(payload);
                    break;
                case Mlp.TYPE_FILE_CHUNK:
                    handleRelayFileChunk(payload);
                    break;
                case Mlp.TYPE_FILE_DONE:
                    fileCountStatic++;
                    statusStatic = "文件发送完成（中继）";
                    sendBroadcast(new Intent(ACTION_STATE));
                    break;
                case Mlp.TYPE_FILE_ACK:
                    // Peer accepted our file offer; nothing else to do.
                    break;
            }
        } catch (Exception e) {
            Log.d(TAG, "relay frame: " + e.getMessage());
        }
    }

    private void startRelayFileReceive(byte[] offerBytes) {
        String json = Mlp.str(offerBytes);
        String id = Mlp.jsonString(json, "id");
        String name = Mlp.jsonString(json, "name");
        String sizeStr = Mlp.jsonString(json, "size");
        if (id == null) return;
        String safe = name == null ? "file.bin" : new File(name).getName();
        File dir = new File(getExternalFilesDir(null), "Received");
        dir.mkdirs();
        try {
            RelayRecv r = new RelayRecv();
            r.id = id; r.name = safe;
            r.size = sizeStr == null ? 0 : Long.parseLong(sizeStr);
            r.got = 0;
            r.fos = new java.io.FileOutputStream(new File(dir, safe));
            r.digest = MessageDigest.getInstance("SHA-256");
            relayRecv = r;
            relayWrite(Mlp.TYPE_FILE_ACK, Mlp.utf8("{\"id\":\"" + Mlp.jsonEscape(id) +
                    "\",\"offset\":0,\"ok\":true}"));
            statusStatic = "文件接收: " + safe;
            sendBroadcast(new Intent(ACTION_STATE));
        } catch (Exception e) {
            Log.d(TAG, "startRelayFileReceive: " + e.getMessage());
        }
    }

    private void handleRelayFileChunk(byte[] payload) {
        RelayRecv r = relayRecv;
        if (r == null || payload.length < 20) return;
        try {
            long offset = ((long)(payload[16] & 0xFF) << 24) | ((long)(payload[17] & 0xFF) << 16) |
                    ((long)(payload[18] & 0xFF) << 8) | (long)(payload[19] & 0xFF);
            int len = payload.length - 20;
            if (offset == r.got) {
                r.fos.write(payload, 20, len);
                r.digest.update(payload, 20, len);
                r.got += len;
            }
            if (r.got >= r.size && r.size > 0) {
                r.fos.flush();
                r.fos.close();
                String sha = Mlp.hex(r.digest.digest());
                relayWrite(Mlp.TYPE_FILE_DONE, Mlp.utf8("{\"id\":\"" + Mlp.jsonEscape(r.id) +
                        "\",\"sha256\":\"" + sha + "\"}"));
                fileCountStatic++;
                statusStatic = "文件已接收: " + r.name;
                sendBroadcast(new Intent(ACTION_STATE));
                relayRecv = null;
            }
        } catch (Exception e) {
            Log.d(TAG, "handleRelayFileChunk: " + e.getMessage());
        }
    }

    /** Write a frame to the active relay session (thread-safe). */
    private static boolean relayWrite(byte type, byte[] payload) {
        synchronized (relayLock) {
            if (relayOut == null || relayKey == null) return false;
            try {
                Mlp.writeFrame(relayOut, type, payload, relayKey);
                return true;
            } catch (Exception e) {
                return false;
            }
        }
    }

    private Notification buildNotification(String text) {
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O) {
            NotificationChannel ch = new NotificationChannel("morelink", "MoreLink",
                    NotificationManager.IMPORTANCE_LOW);
            NotificationManager nm = getSystemService(NotificationManager.class);
            if (nm != null) nm.createNotificationChannel(ch);
            return new Notification.Builder(this, "morelink")
                    .setContentTitle("MoreLink")
                    .setContentText(text)
                    .setSmallIcon(android.R.drawable.stat_sys_data_bluetooth)
                    .build();
        }
        return new Notification.Builder(this)
                .setContentTitle("MoreLink")
                .setContentText(text)
                .setSmallIcon(android.R.drawable.stat_sys_data_bluetooth)
                .build();
    }

    private void setStatus(String s) {
        status = s;
        statusStatic = s;
        sendBroadcast(new Intent(ACTION_STATE));
    }

    /** LAN discovery: broadcast hello and listen for peers. */
    private void discoveryLoop() {
        byte[] hello = Mlp.helloJson(myId, myName, "android", Mlp.DATA_PORT).getBytes(StandardCharsets.UTF_8);
        try {
            discovery = new DatagramSocket(null);
            discovery.setReuseAddress(true);
            discovery.bind(new java.net.InetSocketAddress(Mlp.DISCOVERY_PORT));
        } catch (Exception e) {
            Log.e(TAG, "discovery bind", e);
            return;
        }
        InetAddress broadcast;
        try {
            broadcast = InetAddress.getByName("255.255.255.255");
        } catch (Exception e) {
            return;
        }
        // Beacon thread
        pool.execute(() -> {
            while (!stopBeacon.get()) {
                try {
                    DatagramPacket pkt = new DatagramPacket(hello, hello.length, broadcast, Mlp.DISCOVERY_PORT);
                    discovery.send(pkt);
                } catch (Exception ignored) {}
                try { Thread.sleep(3000); } catch (InterruptedException e) { break; }
            }
        });
        // Listen for peer announcements (used to populate UI via preferences cache)
        byte[] buf = new byte[2048];
        while (running.get()) {
            try {
                DatagramPacket pkt = new DatagramPacket(buf, buf.length);
                discovery.receive(pkt);
                String json = new String(pkt.getData(), pkt.getOffset(), pkt.getLength(), StandardCharsets.UTF_8);
                String id = Mlp.jsonString(json, "id");
                String name = Mlp.jsonString(json, "name");
                String platform = Mlp.jsonString(json, "platform");
                if (id == null || id.equals(myId)) continue;
                PeerStore.update(this, id, name, platform, pkt.getAddress().getHostAddress());
                sendBroadcast(new Intent(ACTION_STATE));
            } catch (Exception e) {
                if (!running.get()) break;
            }
        }
    }

    /** TCP server: accept peer connections and process frames. */
    private void serverLoop() {
        try {
            server = new ServerSocket(Mlp.DATA_PORT);
            server.setReuseAddress(true);
        } catch (Exception e) {
            Log.e(TAG, "server bind", e);
            setStatus("端口占用: " + e.getMessage());
            return;
        }
        setStatus("运行中");
        while (running.get()) {
            try {
                Socket c = server.accept();
                pool.execute(() -> handleConnection(c));
            } catch (Exception e) {
                if (!running.get()) break;
            }
        }
    }

    private void handleConnection(Socket socket) {
        try {
            socket.setSoTimeout(30000);
            OutputStream os = socket.getOutputStream();
            InputStream is = new BufferedInputStream(socket.getInputStream());

            // Plaintext HELLO handshake.
            byte[] hello = Mlp.helloJson(myId, myName, "android", Mlp.DATA_PORT).getBytes(StandardCharsets.UTF_8);
            Mlp.writeFrame(os, Mlp.TYPE_HELLO, hello, null);
            byte[] frame = Mlp.readFrame(is, null);
            if (frame == null || Mlp.frameType(frame) != Mlp.TYPE_HELLO) return;
            String peerJson = Mlp.str(Mlp.framePayload(frame));
            String peerId = Mlp.jsonString(peerJson, "id");
            String peerName = Mlp.jsonString(peerJson, "name");
            if (peerId == null) return;
            PeerStore.update(this, peerId, peerName, "windows", socket.getInetAddress().getHostAddress());

            final byte[] sessionKey;
            if (paired && key != null) {
                sessionKey = key;
            } else {
                // Wait for plaintext pairing request.
                frame = Mlp.readFrame(is, null);
                if (frame == null || Mlp.frameType(frame) != Mlp.TYPE_PAIR_REQ) return;
                String req = Mlp.str(Mlp.framePayload(frame));
                String pin = Mlp.jsonString(req, "pin");
                String reqId = Mlp.jsonString(req, "id");
                if (pin == null || pin.length() != 6) return;
                SharedPreferences p = getSharedPreferences(PREFS, MODE_PRIVATE);
                p.edit().putBoolean("paired", true).putString("pin", pin)
                        .putString("peer", reqId == null ? peerId : reqId).apply();
                paired = true;
                key = Mlp.deriveKey(pin, myId, reqId == null ? peerId : reqId);
                sessionKey = key;
                String result = "{\"ok\":true,\"token\":\"ok\"}";
                Mlp.writeFrame(os, Mlp.TYPE_PAIR_RESULT, Mlp.utf8(result), null);
                setStatus("已配对");
            }

            socket.setSoTimeout(0);
            while (running.get()) {
                frame = Mlp.readFrame(is, sessionKey);
                if (frame == null) break;
                byte type = Mlp.frameType(frame);
                byte[] payload = Mlp.framePayload(frame);
                switch (type) {
                    case Mlp.TYPE_PING:
                        Mlp.writeFrame(os, Mlp.TYPE_PONG, new byte[0], sessionKey);
                        break;
                    case Mlp.TYPE_CLIP_TEXT:
                        setClipboard(Mlp.str(payload));
                        setStatus("收到剪贴板文本");
                        break;
                    case Mlp.TYPE_FILE_OFFER:
                        String offer = Mlp.str(payload);
                        String xferId = Mlp.jsonString(offer, "id");
                        String fileName = Mlp.jsonString(offer, "name");
                        long size = parseLong(Mlp.jsonString(offer, "size"));
                        receiveFile(os, is, sessionKey, xferId, fileName, size);
                        break;
                }
            }
        } catch (Exception e) {
            Log.d(TAG, "connection ended: " + e.getMessage());
        } finally {
            try { socket.close(); } catch (Exception ignored) {}
        }
    }

    private long parseLong(String s) {
        try { return s == null ? 0 : Long.parseLong(s); } catch (Exception e) { return 0; }
    }

    private void receiveFile(OutputStream os, InputStream is, byte[] sessionKey,
                             String xferId, String fileName, long size) {
        if (xferId == null || fileName == null) return;
        String safe = new File(fileName).getName();
        File dir = new File(getExternalFilesDir(null), "Received");
        dir.mkdirs();
        File dst = new File(dir, safe);
        long got = 0, lastAck = 0;
        try (FileOutputStream fos = new FileOutputStream(dst)) {
            String ack = "{\"id\":\"" + Mlp.jsonEscape(xferId) + "\",\"offset\":0,\"ok\":true}";
            Mlp.writeFrame(os, Mlp.TYPE_FILE_ACK, Mlp.utf8(ack), sessionKey);
            while (got < size) {
                byte[] frame = Mlp.readFrame(is, sessionKey);
                if (frame == null) break;
                if (Mlp.frameType(frame) != Mlp.TYPE_FILE_CHUNK) continue;
                byte[] p = Mlp.framePayload(frame);
                if (p.length < 20) continue;
                long offset = ((long)(p[16] & 0xFF) << 24) | ((long)(p[17] & 0xFF) << 16) |
                        ((long)(p[18] & 0xFF) << 8) | (p[19] & 0xFF);
                byte[] data = Arrays.copyOfRange(p, 20, p.length);
                fos.getChannel().write(java.nio.ByteBuffer.wrap(data), offset);
                got = offset + data.length;
                if (got - lastAck > (1 << 20) || got == size) {
                    String a = "{\"id\":\"" + Mlp.jsonEscape(xferId) + "\",\"offset\":" + got + ",\"ok\":true}";
                    Mlp.writeFrame(os, Mlp.TYPE_FILE_ACK, Mlp.utf8(a), sessionKey);
                    lastAck = got;
                }
            }
            byte[] digest = MessageDigest.getInstance("SHA-256").digest(java.nio.file.Files.readAllBytes(dst.toPath()));
            String done = "{\"id\":\"" + Mlp.jsonEscape(xferId) + "\",\"sha256\":\"" + Mlp.hex(digest) + "\"}";
            Mlp.writeFrame(os, Mlp.TYPE_FILE_DONE, Mlp.utf8(done), sessionKey);
            fileCountStatic++;
            setStatus("文件已接收: " + safe);
            sendBroadcast(new Intent(ACTION_STATE));
        } catch (Exception e) {
            Log.e(TAG, "receiveFile", e);
        }
    }

    private void setClipboard(String text) {
        try {
            ClipboardManager cm = (ClipboardManager) getSystemService(Context.CLIPBOARD_SERVICE);
            if (cm != null) cm.setPrimaryClip(ClipData.newPlainText("MoreLink", text));
        } catch (Exception e) {
            Log.e(TAG, "clipboard", e);
        }
    }

    /** Called by NotificationListener to forward a notification to paired PC. */
    public static void forwardNotification(Context c, String app, String title, String text) {
        SharedPreferences p = c.getSharedPreferences(PREFS, Context.MODE_PRIVATE);
        if (!p.getBoolean("paired", false)) return;
        String id = p.getString("id", "");
        String peer = p.getString("peer", "");
        byte[] key = Mlp.deriveKey(p.getString("pin", ""), id, peer);
        String ip = PeerStore.firstWindowsIp(c);
        if (ip == null) return;
        String json = "{\"app\":\"" + Mlp.jsonEscape(app) + "\",\"title\":\"" + Mlp.jsonEscape(title) +
                "\",\"text\":\"" + Mlp.jsonEscape(text) + "\",\"ts\":" + (System.currentTimeMillis()/1000) + "}";
        try (Socket s = new Socket(ip, Mlp.DATA_PORT)) {
            s.setSoTimeout(8000);
            OutputStream os = s.getOutputStream();
            InputStream is = new BufferedInputStream(s.getInputStream());
            byte[] hello = Mlp.helloJson(id, p.getString("name", "Android"), "android", Mlp.DATA_PORT).getBytes(StandardCharsets.UTF_8);
            Mlp.writeFrame(os, Mlp.TYPE_HELLO, hello, null);
            byte[] f = Mlp.readFrame(is, null);
            if (f == null || Mlp.frameType(f) != Mlp.TYPE_HELLO) return;
            Mlp.writeFrame(os, Mlp.TYPE_NOTIFY, Mlp.utf8(json), key);
            notificationCountStatic++;
            statusStatic = "已同步通知: " + title;
            c.sendBroadcast(new Intent(ACTION_STATE));
        } catch (Exception e) {
            Log.d(TAG, "forward notify failed: " + e.getMessage());
        }
    }

    /** Send clipboard text to a peer (via relay if enabled, else direct). */
    public static String sendClipboard(Context c, String ip, int port, String text) {
        SharedPreferences p = c.getSharedPreferences(PREFS, Context.MODE_PRIVATE);
        if (p.getBoolean("relay_enabled", false)) {
            if (relayWrite(Mlp.TYPE_CLIP_TEXT, Mlp.utf8(text))) {
                statusStatic = "剪贴板已发送（中继）";
                c.sendBroadcast(new Intent(ACTION_STATE));
                return "已发送（中继）";
            }
            return "中继未连接，请稍候";
        }
        if (!p.getBoolean("paired", false)) return "请先配对";
        byte[] key = Mlp.deriveKey(p.getString("pin", ""), p.getString("id", ""), p.getString("peer", ""));
        try (Socket s = new Socket(ip, port)) {
            s.setSoTimeout(8000);
            OutputStream os = s.getOutputStream();
            InputStream is = new BufferedInputStream(s.getInputStream());
            byte[] hello = Mlp.helloJson(p.getString("id", ""), p.getString("name", "Android"),
                    "android", Mlp.DATA_PORT).getBytes(StandardCharsets.UTF_8);
            Mlp.writeFrame(os, Mlp.TYPE_HELLO, hello, null);
            byte[] f = Mlp.readFrame(is, null);
            if (f == null || Mlp.frameType(f) != Mlp.TYPE_HELLO) return "握手失败";
            Mlp.writeFrame(os, Mlp.TYPE_CLIP_TEXT, Mlp.utf8(text), key);
            statusStatic = "剪贴板已发送";
            c.sendBroadcast(new Intent(ACTION_STATE));
            return "已发送";
        } catch (Exception e) {
            return "失败: " + e.getMessage();
        }
    }

    /** Actively pair with a peer: send PAIR_REQ with our PIN and read the result. */
    public static String pairWithPeer(Context c, String ip, int port, String pin) {
        SharedPreferences p = c.getSharedPreferences(PREFS, Context.MODE_PRIVATE);
        String id = p.getString("id", "");
        String name = p.getString("name", "Android");
        try (Socket s = new Socket(ip, port)) {
            s.setSoTimeout(8000);
            OutputStream os = s.getOutputStream();
            InputStream is = new BufferedInputStream(s.getInputStream());
            Mlp.writeFrame(os, Mlp.TYPE_HELLO, Mlp.utf8(Mlp.helloJson(id, name, "android", Mlp.DATA_PORT)), null);
            byte[] f = Mlp.readFrame(is, null);
            if (f == null || Mlp.frameType(f) != Mlp.TYPE_HELLO) return "握手失败";
            String peerJson = Mlp.str(Mlp.framePayload(f));
            String peerId = Mlp.jsonString(peerJson, "id");
            String req = "{\"pin\":\"" + Mlp.jsonEscape(pin) + "\",\"id\":\"" + Mlp.jsonEscape(id) +
                    "\",\"name\":\"" + Mlp.jsonEscape(name) + "\"}";
            Mlp.writeFrame(os, Mlp.TYPE_PAIR_REQ, Mlp.utf8(req), null);
            byte[] res = Mlp.readFrame(is, null);
            if (res == null || Mlp.frameType(res) != Mlp.TYPE_PAIR_RESULT) return "未收到配对结果";
            boolean ok = Mlp.jsonString(Mlp.str(Mlp.framePayload(res)), "ok") != null;
            // The result payload JSON has a boolean; check literal.
            ok = Mlp.str(Mlp.framePayload(res)).contains("\"ok\":true");
            if (!ok) return "对端拒绝了配对";
            p.edit().putString("pin", pin).putBoolean("paired", true)
                    .putString("peer", peerId == null ? "windows-peer" : peerId).apply();
            statusStatic = "已配对 · PIN " + pin;
            c.sendBroadcast(new Intent(ACTION_STATE));
            return "配对成功";
        } catch (Exception e) {
            return "失败: " + e.getMessage();
        }
    }

    /** Send a local file to a peer (via relay if enabled, else direct). */
    public static String sendFile(Context c, String ip, int port, String path, String displayName) {
        SharedPreferences p = c.getSharedPreferences(PREFS, Context.MODE_PRIVATE);
        if (p.getBoolean("relay_enabled", false)) {
            return sendFileRelay(c, path, displayName);
        }
        if (!p.getBoolean("paired", false)) return "请先配对";
        byte[] key = Mlp.deriveKey(p.getString("pin", ""), p.getString("id", ""), p.getString("peer", ""));
        File src = new File(path);
        if (!src.exists()) return "文件不存在";
        long size = src.length();
        try (Socket s = new Socket(ip, port); java.io.FileInputStream fis = new java.io.FileInputStream(src)) {
            s.setSoTimeout(15000);
            OutputStream os = s.getOutputStream();
            InputStream is = new BufferedInputStream(s.getInputStream());
            byte[] hello = Mlp.helloJson(p.getString("id", ""), p.getString("name", "Android"),
                    "android", Mlp.DATA_PORT).getBytes(StandardCharsets.UTF_8);
            Mlp.writeFrame(os, Mlp.TYPE_HELLO, hello, null);
            byte[] f = Mlp.readFrame(is, null);
            if (f == null || Mlp.frameType(f) != Mlp.TYPE_HELLO) return "握手失败";
            String xferId = Mlp.makeId();
            String offer = "{\"id\":\"" + Mlp.jsonEscape(xferId) + "\",\"name\":\"" +
                    Mlp.jsonEscape(displayName == null ? src.getName() : displayName) +
                    "\",\"size\":" + size + ",\"mime\":\"application/octet-stream\"}";
            Mlp.writeFrame(os, Mlp.TYPE_FILE_OFFER, Mlp.utf8(offer), key);
            byte[] ack = Mlp.readFrame(is, key);
            if (ack == null || Mlp.frameType(ack) != Mlp.TYPE_FILE_ACK) return "对端未接受文件";
            byte[] buf = new byte[256 << 10];
            long offset = 0;
            int n;
            while ((n = fis.read(buf)) > 0) {
                byte[] chunk = new byte[20 + n];
                byte[] idb = Mlp.utf8(xferId);
                System.arraycopy(idb, 0, chunk, 0, Math.min(16, idb.length));
                chunk[16] = (byte)(offset >>> 24);
                chunk[17] = (byte)(offset >>> 16);
                chunk[18] = (byte)(offset >>> 8);
                chunk[19] = (byte)(offset);
                System.arraycopy(buf, 0, chunk, 20, n);
                Mlp.writeFrame(os, Mlp.TYPE_FILE_CHUNK, chunk, key);
                offset += n;
            }
            byte[] done = Mlp.readFrame(is, key); // FILE_DONE
            fileCountStatic++;
            statusStatic = "文件已发送: " + (displayName == null ? src.getName() : displayName);
            c.sendBroadcast(new Intent(ACTION_STATE));
            return "已发送";
        } catch (Exception e) {
            return "失败: " + e.getMessage();
        }
    }

    /** Send a file over the active relay session (fire-and-forget; ACK/DONE handled in relay loop). */
    private static String sendFileRelay(Context c, String path, String displayName) {
        if (relayOut == null || relayKey == null) return "中继未连接，请稍候";
        File src = new File(path);
        if (!src.exists()) return "文件不存在";
        final String safe = displayName == null ? src.getName() : displayName;
        final String xferId = Mlp.makeId();
        try {
            String offer = "{\"id\":\"" + Mlp.jsonEscape(xferId) + "\",\"name\":\"" +
                    Mlp.jsonEscape(safe) + "\",\"size\":" + src.length() +
                    ",\"mime\":\"application/octet-stream\"}";
            relayWrite(Mlp.TYPE_FILE_OFFER, Mlp.utf8(offer));
            byte[] buf = new byte[256 << 10];
            long offset = 0;
            java.io.FileInputStream fis = new java.io.FileInputStream(src);
            int n;
            while ((n = fis.read(buf)) > 0) {
                byte[] chunk = new byte[20 + n];
                byte[] idb = Mlp.utf8(xferId);
                System.arraycopy(idb, 0, chunk, 0, Math.min(16, idb.length));
                chunk[16] = (byte)(offset >>> 24);
                chunk[17] = (byte)(offset >>> 16);
                chunk[18] = (byte)(offset >>> 8);
                chunk[19] = (byte)offset;
                System.arraycopy(buf, 0, chunk, 20, n);
                relayWrite(Mlp.TYPE_FILE_CHUNK, chunk);
                offset += n;
            }
            fis.close();
            statusStatic = "文件已发送（中继）: " + safe;
            c.sendBroadcast(new Intent(ACTION_STATE));
            return "已发送（中继）";
        } catch (Exception e) {
            return "失败: " + e.getMessage();
        }
    }

    /** Enable or disable the relay session at runtime. */
    public static void setRelayEnabled(Context c, boolean enabled, String host, int port, String room, String pin) {
        SharedPreferences p = c.getSharedPreferences(PREFS, MODE_PRIVATE);
        p.edit().putBoolean("relay_enabled", enabled).putString("relay_host", host)
                .putInt("relay_port", port).putString("relay_room", room)
                .putString("relay_pin", pin).apply();
        relayWanted.set(enabled);
        if (!enabled) {
            synchronized (relayLock) {
                try { if (relaySocket != null) relaySocket.close(); } catch (Exception ignored) {}
                relaySocket = null; relayOut = null; relayKey = null;
            }
        }
        statusStatic = enabled ? "正在连接中继…" : "中继已关闭";
        c.sendBroadcast(new Intent(ACTION_STATE));
    }

    /** Generate a new pairing PIN and display it. */
    public static String newPin(Context c) {
        String pin = Mlp.makePin();
        c.getSharedPreferences(PREFS, MODE_PRIVATE).edit().putString("pin", pin).apply();
        statusStatic = "配对码: " + pin;
        c.sendBroadcast(new Intent(ACTION_STATE));
        return pin;
    }

    @Override
    public void onDestroy() {
        running.set(false);
        stopBeacon.set(true);
        try { if (server != null) server.close(); } catch (Exception ignored) {}
        try { if (discovery != null) discovery.close(); } catch (Exception ignored) {}
        pool.shutdownNow();
        super.onDestroy();
    }

    @Override
    public IBinder onBind(Intent intent) { return null; }
}
