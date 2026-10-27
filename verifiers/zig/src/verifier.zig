//! Independent SpherePOP Term, Rule & Trace Verifier in Pure Zig
//! Implements SPEC.md Sections 2, 4, and 8 without sharing any code with the Go reducer.
//!
//! Domain separations (exact byte-for-byte match with term/term.go and trace/trace.go):
//!   - Term digest:  SHA256(uvarint(16) || "spherepop-term/1" || [kind] || payload)
//!   - Config hash:  SHA256("spherepop-config/1" || 0x00 || canonical_json || 0x00)
//!   - Trace genesis: SHA256("spherepop-trace/1" || 0x00 || start_digest || 0x00 || config_digest || 0x00)
//!   - Step link:    SHA256(prev_link || 0x00 || rule || 0x00 || path || 0x00 || after_digest || 0x00)

const std = @import("std");
const Sha256 = std.crypto.hash.sha2.Sha256;

pub const Kind = enum(u8) {
    atom = 1,
    constant = 2,
    scope = 3,
    app = 4,
    bracket = 5,
};

pub const Digest = [32]u8;
pub const HexDigest = [64]u8;

const TERM_DOMAIN = "spherepop-term/1";
const CONFIG_DOMAIN = "spherepop-config/1";
const TRACE_FORMAT = "spherepop-trace/1";

fn writeUvarint(hasher: *Sha256, value: u64) void {
    var v = value;
    var buf: [10]u8 = undefined;
    var i: usize = 0;
    while (v >= 0x80) : (i += 1) {
        buf[i] = @as(u8, @truncate(v)) | 0x80;
        v >>= 7;
    }
    buf[i] = @as(u8, @truncate(v));
    hasher.update(buf[0 .. i + 1]);
}

fn writeString(hasher: *Sha256, s: []const u8) void {
    writeUvarint(hasher, s.len);
    hasher.update(s);
}

pub fn digestLeaf(kind: Kind, name: []const u8) Digest {
    var h = Sha256.init(.{});
    writeString(&h, TERM_DOMAIN);
    h.update(&[_]u8{@intFromEnum(kind)});
    writeString(&h, name);
    var out: Digest = undefined;
    h.final(&out);
    return out;
}

pub fn digestApp(f_digest: Digest, x_digest: Digest) Digest {
    var h = Sha256.init(.{});
    writeString(&h, TERM_DOMAIN);
    h.update(&[_]u8{@intFromEnum(Kind.app)});
    h.update(&f_digest);
    h.update(&x_digest);
    var out: Digest = undefined;
    h.final(&out);
    return out;
}

pub fn digestBracket(label: []const u8, body_digest: Digest) Digest {
    var h = Sha256.init(.{});
    writeString(&h, TERM_DOMAIN);
    h.update(&[_]u8{@intFromEnum(Kind.bracket)});
    writeString(&h, label);
    h.update(&body_digest);
    var out: Digest = undefined;
    h.final(&out);
    return out;
}

pub fn toHex(d: Digest) HexDigest {
    return std.fmt.bytesToHex(d, .lower);
}

fn sumZeroSeparated(parts: []const []const u8) HexDigest {
    var h = Sha256.init(.{});
    for (parts) |p| {
        h.update(p);
        h.update(&[_]u8{0});
    }
    var out: Digest = undefined;
    h.final(&out);
    return toHex(out);
}

pub fn configDigest(canonical_config_json: []const u8) HexDigest {
    const parts = [_][]const u8{ CONFIG_DOMAIN, canonical_config_json };
    return sumZeroSeparated(&parts);
}

pub fn traceGenesis(start_digest_hex: []const u8, cfg_digest_hex: []const u8) HexDigest {
    const parts = [_][]const u8{ TRACE_FORMAT, start_digest_hex, cfg_digest_hex };
    return sumZeroSeparated(&parts);
}

pub fn traceLink(
    prev_hex: []const u8,
    rule: []const u8,
    path: []const u8,
    after_hex: []const u8,
) HexDigest {
    const parts = [_][]const u8{ prev_hex, rule, path, after_hex };
    return sumZeroSeparated(&parts);
}

pub const TermNode = struct {
    kind: Kind,
    name: []const u8 = "",
    left: ?*const TermNode = null,
    right: ?*const TermNode = null,
    digest: Digest,

    pub fn initAtom(name: []const u8) TermNode {
        return .{ .kind = .atom, .name = name, .digest = digestLeaf(.atom, name) };
    }

    pub fn initConst(name: []const u8) TermNode {
        return .{ .kind = .constant, .name = name, .digest = digestLeaf(.constant, name) };
    }

    pub fn initScope(label: []const u8) TermNode {
        return .{ .kind = .scope, .name = label, .digest = digestLeaf(.scope, label) };
    }

    pub fn initApp(f: *const TermNode, x: *const TermNode) TermNode {
        return .{
            .kind = .app,
            .left = f,
            .right = x,
            .digest = digestApp(f.digest, x.digest),
        };
    }

    pub fn initBracket(label: []const u8, body: *const TermNode) TermNode {
        return .{
            .kind = .bracket,
            .name = label,
            .left = body,
            .digest = digestBracket(label, body.digest),
        };
    }
};

test "SPEC §2 & §8: independent Zig verification of 'S K K x' digests and trace hash-chain" {
    const s_node = TermNode.initConst("S");
    const k_node = TermNode.initConst("K");
    const x_node = TermNode.initAtom("x");

    // Start term: ((S K) K) x
    const sk = TermNode.initApp(&s_node, &k_node);
    const skk = TermNode.initApp(&sk, &k_node);
    const start = TermNode.initApp(&skk, &x_node);

    const start_hex = toHex(start.digest);
    try std.testing.expectEqualStrings(
        "381945db076b66348394c22dba6a47677b3af80e056b7665d59ad12fc304aa83",
        &start_hex,
    );

    // Step 1 (Rule S): S K K x -> (K x) (K x)
    const kx = TermNode.initApp(&k_node, &x_node);
    const step1_term = TermNode.initApp(&kx, &kx);
    const step1_after_hex = toHex(step1_term.digest);
    try std.testing.expectEqualStrings(
        "a920c3a3388f4eeceb265437e40957f5000b1325616ddbd1f230e9e2c5a36ab2",
        &step1_after_hex,
    );

    // Step 2 (Rule K): (K x) (K x) -> x
    const final_hex = toHex(x_node.digest);
    try std.testing.expectEqualStrings(
        "07685251c255bb056e562da8ea71975e722286dc4f4d243210b102adbd3c1395",
        &final_hex,
    );

    // Verify trace hash chain against Go's spherepop reduce 'S K K x' --out trace.json
    const default_cfg_json =
        "{\"root\":[\"W\",\"K\",\"C\",\"S\"],\"gated\":[\"W\",\"K\",\"C\"],\"absorb\":\"u\",\"sc\":\"any\"}";
    const cfg_hex = configDigest(default_cfg_json);
    const gen_hex = traceGenesis(&start_hex, &cfg_hex);

    const link1 = traceLink(&gen_hex, "S", "", &step1_after_hex);
    try std.testing.expectEqualStrings(
        "74cfcedff61e9217d34102cce107fb59d0657f96f1402df29c701f319827bc27",
        &link1,
    );

    const link2 = traceLink(&link1, "K", "", &final_hex);
    try std.testing.expectEqualStrings(
        "5c8dd5ef186dece361588fd37c45305dc52b15094f41ed920b836c0e1abb1109",
        &link2,
    );
}

test "SPEC §4 & §17: scoped bracket and safe-scope witness digests (sc_x (<I>_x a))" {
    const i_node = TermNode.initConst("I");
    const a_node = TermNode.initAtom("a");
    const sc_x = TermNode.initScope("x");
    const br_i = TermNode.initBracket("x", &i_node);
    const br_i_a = TermNode.initApp(&br_i, &a_node);
    const witness = TermNode.initApp(&sc_x, &br_i_a);

    // Verify structural determinism and non-collision across distinct node kinds
    try std.testing.expect(!std.mem.eql(u8, &i_node.digest, &a_node.digest));
    try std.testing.expect(!std.mem.eql(u8, &br_i.digest, &sc_x.digest));
    try std.testing.expect(witness.digest.len == 32);
}

// 32-byte 0x62-gate frame sealing and SIMD xorFold (wupz parity)

pub const GATE_0X62: u8 = 0x62;
pub const FRAME_ABI_V1: u8 = 0x01;

pub fn xorFoldScalar(bytes: []const u8) u64 {
    var acc: u64 = 0;
    var i: usize = 0;
    while (i + 8 <= bytes.len) : (i += 8) {
        acc ^= std.mem.readInt(u64, bytes[i..][0..8], .little);
    }
    while (i < bytes.len) : (i += 1) {
        acc ^= @as(u64, bytes[i]);
    }
    return acc;
}

pub fn xorFold(bytes: []const u8) u64 {
    var acc: u64 = 0;
    var i: usize = 0;
    while (i + 32 <= bytes.len) : (i += 32) {
        const words: @Vector(4, u64) = .{
            std.mem.readInt(u64, bytes[i..][0..8], .little),
            std.mem.readInt(u64, bytes[i + 8 ..][0..8], .little),
            std.mem.readInt(u64, bytes[i + 16 ..][0..8], .little),
            std.mem.readInt(u64, bytes[i + 24 ..][0..8], .little),
        };
        acc ^= @reduce(.Xor, words);
    }
    while (i + 8 <= bytes.len) : (i += 8) {
        acc ^= std.mem.readInt(u64, bytes[i..][0..8], .little);
    }
    while (i < bytes.len) : (i += 1) {
        acc ^= @as(u64, bytes[i]);
    }
    return acc;
}

pub fn sealFrame32B(raw: []const u8, packed_bytes: []const u8, gate: u8) [32]u8 {
    var frame: [32]u8 = @splat(0);
    std.mem.writeInt(u64, frame[0..8], xorFold(raw), .little);
    std.mem.writeInt(u64, frame[8..16], xorFold(packed_bytes), .little);
    std.mem.writeInt(u32, frame[16..20], @truncate(raw.len), .little);
    std.mem.writeInt(u32, frame[20..24], @truncate(packed_bytes.len), .little);
    frame[24] = gate;
    frame[25] = FRAME_ABI_V1;
    var prefix_le: [8]u8 = undefined;
    std.mem.writeInt(u64, &prefix_le, xorFold(frame[0..26]), .little);
    @memcpy(frame[26..32], prefix_le[0..6]);
    return frame;
}

pub fn checkFrame32B(frame: *const [32]u8, raw: []const u8, packed_bytes: []const u8, gate: u8) bool {
    if (frame[24] != gate or frame[25] != FRAME_ABI_V1) return false;
    const expected = sealFrame32B(raw, packed_bytes, gate);
    var diff: u8 = 0;
    for (frame, expected) |a, b| diff |= a ^ b;
    return diff == 0;
}

test "32B 0x62-gate frame seal over trace links and 3761/3314 payload" {
    var raw: [3761]u8 = @splat(0x62);
    var packed_bytes: [3314]u8 = @splat(0x33);
    raw[0] = 0x74;
    packed_bytes[0] = 0x5c;

    var frame = sealFrame32B(&raw, &packed_bytes, GATE_0X62);
    try std.testing.expectEqual(GATE_0X62, frame[24]);
    try std.testing.expectEqual(xorFoldScalar(&raw), xorFold(&raw));
    try std.testing.expect(checkFrame32B(&frame, &raw, &packed_bytes, GATE_0X62));

    frame[31] ^= 0x01;
    try std.testing.expect(!checkFrame32B(&frame, &raw, &packed_bytes, GATE_0X62));
}
