#!/usr/bin/env perl
# Independent Perl 5 verifier for SpherePOP term digests, trace hash-chains,
# BFP-64 Kleene AND/NAND bitplanes, and 0x62-gate 32-byte frame seals.

use strict;
use warnings;
use Digest::SHA qw(sha256 sha256_hex);

use constant {
    KIND_ATOM    => 1,
    KIND_CONST   => 2,
    KIND_SCOPE   => 3,
    KIND_APP     => 4,
    KIND_BRACKET => 5,
    GATE_0X62    => 0x62,
    FRAME_ABI_V1 => 0x01,
};

sub encode_uvarint {
    my ($v) = @_;
    my @bytes;
    while ($v >= 0x80) {
        push @bytes, (($v & 0x7F) | 0x80);
        $v >>= 7;
    }
    push @bytes, ($v & 0x7F);
    return pack('C*', @bytes);
}

sub encode_str {
    my ($s) = @_;
    return encode_uvarint(length($s)) . $s;
}

sub digest_leaf {
    my ($kind, $name) = @_;
    return sha256(
        encode_str("spherepop-term/1")
        . pack('C', $kind)
        . encode_str($name)
    );
}

sub digest_app {
    my ($f_dig, $x_dig) = @_;
    return sha256(
        encode_str("spherepop-term/1")
        . pack('C', KIND_APP)
        . $f_dig
        . $x_dig
    );
}

sub sum_zero_sep {
    my (@parts) = @_;
    my $buf = '';
    for my $p (@parts) {
        $buf .= $p . "\x00";
    }
    return sha256_hex($buf);
}

sub xor_fold_bytes {
    my ($bytes) = @_;
    my @b = unpack('C*', $bytes);
    my $n = scalar(@b);
    my $full = $n & ~7;
    my @acc = (0) x 8;
    for (my $i = 0; $i < $full; $i++) {
        $acc[$i & 7] ^= $b[$i];
    }
    for (my $i = $full; $i < $n; $i++) {
        $acc[0] ^= $b[$i];
    }
    return pack('C8', @acc);
}

sub seal_frame_32b {
    my ($raw, $packed, $gate) = @_;
    $gate //= GATE_0X62;
    my $hdr26 = xor_fold_bytes($raw)
              . xor_fold_bytes($packed)
              . pack('V', length($raw))
              . pack('V', length($packed))
              . pack('C2', $gate & 0xFF, FRAME_ABI_V1);
    my $prefix8 = xor_fold_bytes($hdr26);
    return $hdr26 . substr($prefix8, 0, 6);
}

sub check_frame_32b {
    my ($frame, $raw, $packed, $gate) = @_;
    $gate //= GATE_0X62;
    return 0 unless defined($frame) && length($frame) == 32;
    my $expected = seal_frame_32b($raw, $packed, $gate);
    my @a = unpack('C32', $frame);
    my @b = unpack('C32', $expected);
    my $diff = 0;
    for my $i (0 .. 31) {
        $diff |= ($a[$i] ^ $b[$i]);
    }
    return $diff == 0 ? 1 : 0;
}

# Verify SPEC.md Section 2 and Section 8 on 'S K K x'
my $s_dig = digest_leaf(KIND_CONST, "S");
my $k_dig = digest_leaf(KIND_CONST, "K");
my $x_dig = digest_leaf(KIND_ATOM,  "x");

my $sk_dig    = digest_app($s_dig, $k_dig);
my $skk_dig   = digest_app($sk_dig, $k_dig);
my $start_dig = digest_app($skk_dig, $x_dig);

my $start_hex = unpack('H*', $start_dig);
die "start digest mismatch: $start_hex"
    unless $start_hex eq "381945db076b66348394c22dba6a47677b3af80e056b7665d59ad12fc304aa83";

my $kx_dig    = digest_app($k_dig, $x_dig);
my $step1_dig = digest_app($kx_dig, $kx_dig);
my $step1_hex = unpack('H*', $step1_dig);
die "step1 digest mismatch: $step1_hex"
    unless $step1_hex eq "a920c3a3388f4eeceb265437e40957f5000b1325616ddbd1f230e9e2c5a36ab2";

my $final_hex = unpack('H*', $x_dig);
die "final digest mismatch: $final_hex"
    unless $final_hex eq "07685251c255bb056e562da8ea71975e722286dc4f4d243210b102adbd3c1395";

my $cfg_json = '{"root":["W","K","C","S"],"gated":["W","K","C"],"absorb":"u","sc":"any"}';
my $cfg_hex  = sum_zero_sep("spherepop-config/1", $cfg_json);
my $gen_hex  = sum_zero_sep("spherepop-trace/1", $start_hex, $cfg_hex);
my $link1    = sum_zero_sep($gen_hex, "S", "", $step1_hex);
my $link2    = sum_zero_sep($link1, "K", "", $final_hex);

die "link1 mismatch: $link1"
    unless $link1 eq "74cfcedff61e9217d34102cce107fb59d0657f96f1402df29c701f319827bc27";
die "link2 mismatch: $link2"
    unless $link2 eq "5c8dd5ef186dece361588fd37c45305dc52b15094f41ed920b836c0e1abb1109";

my $raw3761    = "t" . ("b" x 3760);
my $packed3314 = "\\" . ("3" x 3313);
my $frame32    = seal_frame_32b($raw3761, $packed3314, GATE_0X62);
die "0x62 frame check failed" unless check_frame_32b($frame32, $raw3761, $packed3314, GATE_0X62) == 1;

print "ok 1..6 - Perl SpherePOP verifier & 0x62 32B frame seal passed\n";
