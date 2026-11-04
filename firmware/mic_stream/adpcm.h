// IMA ADPCM, 4 bits per sample: a quarter of the PCM traffic. The same block format as server/adpcm.go:
// int16 first sample, uint8 step index, uint8 n = data bytes, then n bytes = 2n samples (low nibble first).
#pragma once
#include <stdint.h>

static const int8_t imaIndex[16] = {-1, -1, -1, -1, 2, 4, 6, 8, -1, -1, -1, -1, 2, 4, 6, 8};
static const int16_t imaStep[89] = {
  7, 8, 9, 10, 11, 12, 13, 14, 16, 17, 19, 21, 23, 25, 28, 31, 34, 37, 41, 45,
  50, 55, 60, 66, 73, 80, 88, 97, 107, 118, 130, 143, 157, 173, 190, 209, 230, 253, 279, 307,
  337, 371, 408, 449, 494, 544, 598, 658, 724, 796, 876, 963, 1060, 1166, 1282, 1411, 1552, 1707, 1878, 2066,
  2272, 2499, 2749, 3024, 3327, 3660, 4026, 4428, 4871, 5358, 5894, 6484, 7132, 7845, 8630, 9493, 10442, 11487, 12635, 13899,
  15289, 16818, 18500, 20350, 22385, 24623, 27086, 29794, 32767};

struct Ima {
  int pred, index;
  int16_t decode(uint8_t code) {
    int step = imaStep[index], diff = step >> 3;
    if (code & 4) diff += step;
    if (code & 2) diff += step >> 1;
    if (code & 1) diff += step >> 2;
    pred += (code & 8) ? -diff : diff;
    if (pred > 32767) pred = 32767;
    if (pred < -32768) pred = -32768;
    index += imaIndex[code];
    if (index < 0) index = 0;
    if (index > 88) index = 88;
    return (int16_t)pred;
  }
  uint8_t encode(int16_t s) {
    int diff = s - pred, step = imaStep[index];
    uint8_t code = 0;
    if (diff < 0) { code = 8; diff = -diff; }
    if (diff >= step) { code |= 4; diff -= step; }
    if (diff >= step >> 1) { code |= 2; diff -= step >> 1; }
    if (diff >= step >> 2) code |= 1;
    decode(code);                                     // keeps the encoder's prediction where the decoder's will be
    return code;
  }
};

// One block of n samples (n even, up to 510) into out; returns the bytes written (4 + n/2).
// index carries the step over to the next block.
inline int adpcmEncodeBlock(const int16_t *pcm, int n, uint8_t *out, int &index) {
  Ima st = {pcm[0], index};
  out[0] = (uint16_t)pcm[0] & 0xFF;
  out[1] = (uint16_t)pcm[0] >> 8;
  out[2] = index;
  out[3] = n / 2;
  for (int k = 0; k < n; k += 2) out[4 + k / 2] = st.encode(pcm[k]) | (st.encode(pcm[k + 1]) << 4);
  index = st.index;
  return 4 + n / 2;
}
