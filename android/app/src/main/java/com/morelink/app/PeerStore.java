package com.morelink.app;

import android.content.Context;
import android.content.SharedPreferences;

import org.json.JSONArray;
import org.json.JSONObject;

import java.util.Iterator;

/** Persists discovered peers and provides cached lookup. */
public final class PeerStore {
    private PeerStore() {}

    public static void update(Context c, String id, String name, String platform, String ip) {
        SharedPreferences p = c.getSharedPreferences(LinkService.PREFS, Context.MODE_PRIVATE);
        String json = p.getString("peers", "[]");
        try {
            JSONArray arr = new JSONArray(json);
            boolean found = false;
            for (int i = 0; i < arr.length(); i++) {
                JSONObject o = arr.getJSONObject(i);
                if (id.equals(o.optString("id"))) {
                    o.put("name", name == null ? o.optString("name") : name);
                    o.put("platform", platform == null ? o.optString("platform") : platform);
                    o.put("ip", ip);
                    o.put("lastSeen", System.currentTimeMillis() / 1000);
                    found = true;
                    break;
                }
            }
            if (!found) {
                JSONObject o = new JSONObject();
                o.put("id", id);
                o.put("name", name == null ? id : name);
                o.put("platform", platform == null ? "unknown" : platform);
                o.put("ip", ip);
                o.put("lastSeen", System.currentTimeMillis() / 1000);
                arr.put(o);
            }
            p.edit().putString("peers", arr.toString()).apply();
        } catch (Exception e) {
            p.edit().putString("peers", "[]").apply();
        }
    }

    public static String peersJson(Context c) {
        return c.getSharedPreferences(LinkService.PREFS, Context.MODE_PRIVATE).getString("peers", "[]");
    }

    public static String firstWindowsIp(Context c) {
        SharedPreferences p = c.getSharedPreferences(LinkService.PREFS, Context.MODE_PRIVATE);
        String id = p.getString("peer", "");
        String fallback = null;
        try {
            JSONArray arr = new JSONArray(p.getString("peers", "[]"));
            for (int i = 0; i < arr.length(); i++) {
                JSONObject o = arr.getJSONObject(i);
                String ip = o.optString("ip");
                if (ip.isEmpty()) continue;
                if (id.equals(o.optString("id")) || "windows".equals(o.optString("platform"))) return ip;
                if (fallback == null) fallback = ip;
            }
        } catch (Exception ignored) {}
        return fallback;
    }

    public static String peerIp(Context c, String id) {
        try {
            JSONArray arr = new JSONArray(peersJson(c));
            for (int i = 0; i < arr.length(); i++) {
                JSONObject o = arr.getJSONObject(i);
                if (id.equals(o.optString("id"))) return o.optString("ip");
            }
        } catch (Exception ignored) {}
        return null;
    }
}
