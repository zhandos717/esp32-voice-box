// Preview only: the enclosure with simplified buttons on the lid.
// Button sizes other than the 57 mm housing are placeholders (not measured).
include <enclosure.scad>

btn_flange_d = 61;   // placeholder
btn_flange_h = 3;    // placeholder
btn_dome_d = 50;     // placeholder
btn_dome_h = 12;     // placeholder

// arcade button: the housing goes through the lid, the flange rests on top, the red dome above it
module arcade_button() {
    translate([btn_big[0], btn_big[1], H - btn_body_depth])
        color([0.15, 0.15, 0.15]) cylinder(d = 57, h = btn_body_depth + lid_t, $fn = 96);
    translate([btn_big[0], btn_big[1], H + lid_t])
        color([0.1, 0.1, 0.1]) cylinder(d = btn_flange_d, h = btn_flange_h, $fn = 96);
    translate([btn_big[0], btn_big[1], H + lid_t + btn_flange_h])
        color([0.9, 0.15, 0.15]) scale([1, 1, btn_dome_h / (btn_dome_d / 2)])
            sphere(r = btn_dome_d / 2, $fn = 48);
}

// LED: a small lens above the lid
module led_lens() {
    translate([led[0], led[1], H + lid_t])
        color([1, 0.7, 0.1]) cylinder(d = led_d, h = 3, $fn = 24);
}

// microphone board inside the holder, sound port facing the grille
module mic_board() {
    translate([mic_c[0], mic_c[1], H - mic_h + 1.6])
        color([0.5, 0.2, 0.7]) cylinder(d = mic_d, h = 1.6, $fn = 48);
}

arcade_button();
led_lens();
mic_board();
