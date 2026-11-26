//! Independent SpherePOP Term, Rule, Trace & UltraGraph Verifier in Pure Zig
//! Implements SPEC.md Sections 2, 4, and 8 plus P0/P1 Zero-Float p/q Law (`Ratio`,
//! `Q16`), Molloy–Reed percolation gate, Rung 0 Bonobo Resonance (3/2 -> +1 trit),
//! and Shadow Entanglement Coupling without sharing any code with the Go reducer.

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

// ============================================================================
// Zero-Float p/q Law: Canonical Reduced Rationals, Q16.16 & UltraGraph Gates
// ============================================================================

pub fn gcd64(a_in: u64, b_in: u64) u64 {
    var a = a_in;
    var b = b_in;
    while (b != 0) {
        const r = a % b;
        a = b;
        b = r;
    }
    return a;
}

pub const Ratio = struct {
    p: u64,
    q: u64,

    pub fn init(p: u64, q: u64) !Ratio {
        if (q == 0) return error.ZeroDenominator;
        if (p == 0) return .{ .p = 0, .q = 1 };
        const g = gcd64(p, q);
        return .{ .p = p / g, .q = q / g };
    }

    pub fn order(self: Ratio, other: Ratio) std.math.Order {
        const lhs: u128 = @as(u128, self.p) * @as(u128, other.q);
        const rhs: u128 = @as(u128, other.p) * @as(u128, self.q);
        return std.math.order(lhs, rhs);
    }

    pub fn isBonoboResonance(self: Ratio) bool {
        return self.p == 3 and self.q == 2;
    }
};

pub const Q16 = i32;
pub const Q16_SHIFT: u5 = 16;
pub const Q16_ONE: Q16 = 1 << Q16_SHIFT;

pub fn q16Add(a: Q16, b: Q16) Q16 {
    return a +% b;
}

pub fn q16Sub(a: Q16, b: Q16) Q16 {
    return a -% b;
}

pub fn q16Mul(a: Q16, b: Q16) Q16 {
    const prod: i64 = @as(i64, a) * @as(i64, b);
    return @truncate(prod >> Q16_SHIFT);
}

pub const PercolationRegime = enum {
    subcritical,
    critical,
    supercritical,
};

pub const PercolationReport = struct {
    kappa: Ratio,
    mean_degree: Ratio,
    resonance_trit: i8,
    regime: PercolationRegime,
    transitive_risk: bool,
};

pub fn molloyReedKappa(
    allocator: std.mem.Allocator,
    vertices: usize,
    edges: []const [2]usize,
) !PercolationReport {
    for (edges) |e| {
        if (e[0] >= vertices or e[1] >= vertices or e[0] == e[1]) {
            return error.InvalidGraph;
        }
    }
    if (vertices == 0 or edges.len == 0) {
        return .{
            .kappa = .{ .p = 0, .q = 1 },
            .mean_degree = .{ .p = 0, .q = 1 },
            .resonance_trit = 0,
            .regime = .subcritical,
            .transitive_risk = false,
        };
    }

    const deg = try allocator.alloc(u64, vertices);
    defer allocator.free(deg);
    @memset(deg, 0);

    for (edges) |e| {
        deg[e[0]] += 1;
        deg[e[1]] += 1;
    }

    var sum_k: u64 = 0;
    var sum_k2: u64 = 0;
    for (deg) |d| {
        sum_k += d;
        sum_k2 += d * d;
    }

    const mean_deg = try Ratio.init(sum_k, @intCast(vertices));
    const kappa = if (sum_k == 0) Ratio{ .p = 0, .q = 1 } else try Ratio.init(sum_k2, sum_k);
    const crit = Ratio{ .p = 2, .q = 1 };

    return switch (kappa.order(crit)) {
        .lt => .{
            .kappa = kappa,
            .mean_degree = mean_deg,
            .resonance_trit = if (kappa.isBonoboResonance()) 1 else 0,
            .regime = .subcritical,
            .transitive_risk = false,
        },
        .eq => .{
            .kappa = kappa,
            .mean_degree = mean_deg,
            .resonance_trit = 0,
            .regime = .critical,
            .transitive_risk = false,
        },
        .gt => .{
            .kappa = kappa,
            .mean_degree = mean_deg,
            .resonance_trit = -1,
            .regime = .supercritical,
            .transitive_risk = true,
        },
    };
}

pub fn checkCollapseGate(
    allocator: std.mem.Allocator,
    vertices: usize,
    edges: []const [2]usize,
) !PercolationReport {
    const rep = try molloyReedKappa(allocator, vertices, edges);
    if (rep.transitive_risk) return error.PercolationRisk;
    return rep;
}

pub fn shadowEntanglementRatio(forward_deg: u64, reverse_deg: u64) !Ratio {
    if (forward_deg == 0 and reverse_deg == 0) return .{ .p = 0, .q = 1 };
    const diff = if (forward_deg >= reverse_deg) forward_deg - reverse_deg else reverse_deg - forward_deg;
    return Ratio.init(diff, forward_deg + reverse_deg);
}

test "SPEC §2 & §8: independent Zig verification of 'S K K x' digests and trace hash-chain" {
    const s_node = TermNode.initConst("S");
    const k_node = TermNode.initConst("K");
    const x_node = TermNode.initAtom("x");

    const sk = TermNode.initApp(&s_node, &k_node);
    const skk = TermNode.initApp(&sk, &k_node);
    const start = TermNode.initApp(&skk, &x_node);

    const start_hex = toHex(start.digest);
    try std.testing.expectEqualStrings(
        "381945db076b66348394c22dba6a47677b3af80e056b7665d59ad12fc304aa83",
        &start_hex,
    );

    const kx = TermNode.initApp(&k_node, &x_node);
    const step1_term = TermNode.initApp(&kx, &kx);
    const step1_after_hex = toHex(step1_term.digest);
    try std.testing.expectEqualStrings(
        "a920c3a3388f4eeceb265437e40957f5000b1325616ddbd1f230e9e2c5a36ab2",
        &step1_after_hex,
    );

    const final_hex = toHex(x_node.digest);
    try std.testing.expectEqualStrings(
        "07685251c255bb056e562da8ea71975e722286dc4f4d243210b102adbd3c1395",
        &final_hex,
    );

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

    try std.testing.expect(!std.mem.eql(u8, &i_node.digest, &a_node.digest));
    try std.testing.expect(!std.mem.eql(u8, &br_i.digest, &sc_x.digest));
    try std.testing.expect(witness.digest.len == 32);
}

test "UltraGraph P0/P1: Zero-Float p/q percolation, Bonobo 3/2 resonance, entanglement & Q16.16" {
    const alloc = std.testing.allocator;

    // percolation-001: P3 path -> kappa = 3/2 (Rung 0 Bonobo resonance, trit = +1)
    const p3_edges = [_][2]usize{ .{ 0, 1 }, .{ 1, 2 } };
    const rep1 = try checkCollapseGate(alloc, 3, &p3_edges);
    try std.testing.expectEqual(@as(u64, 3), rep1.kappa.p);
    try std.testing.expectEqual(@as(u64, 2), rep1.kappa.q);
    try std.testing.expectEqual(@as(i8, 1), rep1.resonance_trit);
    try std.testing.expectEqual(PercolationRegime.subcritical, rep1.regime);

    // percolation-002: K4 complete graph -> kappa = 3/1 -> error.PercolationRisk
    const k4_edges = [_][2]usize{
        .{ 0, 1 }, .{ 0, 2 }, .{ 0, 3 },
        .{ 1, 2 }, .{ 1, 3 }, .{ 2, 3 },
    };
    try std.testing.expectError(error.PercolationRisk, checkCollapseGate(alloc, 4, &k4_edges));

    // unitary-001: |24 - 1| / (24 + 1) = 23/25
    const ent = try shadowEntanglementRatio(24, 1);
    try std.testing.expectEqual(@as(u64, 23), ent.p);
    try std.testing.expectEqual(@as(u64, 25), ent.q);

    // zero-float-001: 0x00018000 * 0x00020000 = 0x00030000 (196608)
    try std.testing.expectEqual(@as(Q16, 0x0003_0000), q16Mul(0x0001_8000, 0x0002_0000));
}
