// Enclosure for the mic board: ESP32 DevKit, INMP441, MAX98357A + speaker, arcade record button, 18650 cell.
// No screws: the lid, the ESP32, the mic and the speaker all go in on snaps and ribs.
// All sizes in mm. Dimensions of the boards and parts are estimates: measure yours and adjust.
// Snap parts are thin on purpose and flex a lot: print the lid and the body in PETG (PLA is brittle for snaps).
// Open in OpenSCAD, set `part`, then export STL (F6 / Render, File > Export).

part = "both";   // "body" | "lid" (print orientation) | "print" (both side by side) | "both" (assembled view)

// outer box
L = 100;         // X
W = 86;          // Y: wider so that the speaker (front) and the 57 mm button housing (back) fit side by side
H = 80;          // body height without lid: the button housing goes 60 mm below the lid, so its bottom is at z=H-60=20, above the ESP32 and the amp
wall = 2.5;
floor_t = 2.5;
r = 4;           // corner radius
lid_t = 3;       // top plate thickness
lip_h = 4;       // lid skirt depth, goes inside the walls
clr = 0.2;       // fit clearance of the skirt

// lid snap tabs: hang from the lid, a hook on each catches a pocket in the body wall
tab_len = 10;    // long tab = low strain when it flexes
tab_t = 1.4;
hook_d = 0.9;    // hook sticks out of the tab; 0.2 of that is the clearance to the wall
// [wall axis, side, position along the wall, tab width]; the front wall is shorter, the speaker ribs are there
snap_tabs = [
    ["x",  1, -18, 10], ["x",  1, 18, 10],
    ["x", -1, -18, 10], ["x", -1, 18, 10],
    ["y",  1, -25, 12], ["y",  1, 25, 12],
    ["y", -1, -41.5, 5], ["y", -1, 41.5, 5]
];

// ESP32 DevKit (51.5 x 28.5 mm) lies along X, USB end at +X (right wall)
esp_pos = [0, 14];
esp_len = 51.5;
standoff_h = 4;
esp_holes = [[-22, -11], [-22, 11], [22, -11], [22, 11]];   // pegs under the board, no screws
esp_clips = [[1, -13.5], [1, 13.5], [-1, 0]];              // [end of the board, offset along Y]

// MAX98357A amp (17.6 x 18.7 mm), back-left corner; the terminal edge faces the front wall (the speaker)
amp_pos = [-36, 12];
amp_h = 18.7;          // along Y
amp_hole_dx = 12.5;    // the two board holes, 2.2 mm from the terminal edge (read off the board drawing)

// 18650 cell lying on the floor along X, in a saddle with end stops
use_battery = false;                       // no cell yet: leave the saddle and its clips out
bat_d = 18.6;                              // 18650 is 18 mm, with a little clearance
cell_len = 65.2;
stop_t = 3.5;                              // thick end stops: they take a knock
bat_len = cell_len + 2 * stop_t + 0.8;
bat_w = 22;
bat_pos = [-4, -17];                       // saddle center on the floor (x, y)

// three posts for strain relief: the wires from the lid controls are woven between them (S-bend, or a zip tie),
// so a tug on the wire is taken by the posts and does not reach the solder joints
wire_posts = [[40.5, -26], [40.5, -16], [40.5, -6]];
wire_post_d = 4.5;
wire_post_h = 14;
wire_hole_d = 6;     // the wire goes through the right wall here, low, then around the posts
wire_hole_z = 8;

// lid controls (absolute XY on the lid)
btn_big   = [-13.5, 9];     btn_hole_d = 57.4;  // arcade button: the 57 mm housing goes through the lid, its flange rests on top,
                                            // its own clips hold it from below
btn_body_depth = 60;                        // how far the housing goes below the lid (measured)
use_led_hole = false;
led       = [38, 22];   led_d = 5;          // "new recordings" LED
btn2      = [30.5, -16];  btn2_hole_d = 12.0; // "next record": 12 x 12 mm tact switch under a round cap
tact      = 12;  tact_body_h = 6.0;  tact_gap = 1.0;   // switch body size, gap between its top and the lid
mic_c     = [37, 6];                        // round mic board center (14.1 mm), port grille around it
mic_d     = 14.1;
mic_h     = 8;                              // legs under the lid: the longer, the softer they flex

// speaker on the front wall (Y = -W/2): frame 69.4 x 31.3 mm, above the cell and the ESP32
spk_w = 69.4; spk_h = 31.3; spk_d = 16.4;   // frame size and depth
spk_x = 0;
spk_z = 39.5;

module rrect(sx, sy, h, rr = r) {
    hull() for (i = [-1, 1], j = [-1, 1])
        translate([i * (sx/2 - rr), j * (sy/2 - rr), 0]) cylinder(r = rr, h = h, $fn = 40);
}

// origin on the inner face of the wall at the lid underside, local +Y points into the wall
module at_snap_tab(t) {
    ax = t[0]; s = t[1]; pos = t[2];
    face = ax == "x" ? s * (L/2 - wall) : s * (W/2 - wall);
    ang = ax == "x" ? -90 * s : (s > 0 ? 0 : 180);
    translate(ax == "x" ? [face, pos, H] : [pos, face, H])
        rotate([0, 0, ang]) children();
}

module battery_saddle() {
    translate([bat_pos[0], bat_pos[1], floor_t])
        difference() {
            union() {
                translate([-bat_len/2, -bat_w/2, 0]) cube([bat_len, bat_w, bat_d/2 + 1.5]);
                for (sx = [-1, 1])
                    translate([sx * (bat_len/2 - stop_t/2) - stop_t/2, -bat_w/2, 0])
                        cube([stop_t, bat_w, bat_d + 1]);
            }
            translate([-bat_len, 0, bat_d/2 + 0.3]) rotate([0, 90, 0]) cylinder(d = bat_d, h = bat_len * 2, $fn = 48);
        }
}

// 18650 snap clip: a thin arm stands next to the saddle, a wedge-shaped hook at its top goes over the shoulder of the cell.
// The cell is pressed in from above and spreads the arms; the long arm keeps the strain low.
module battery_clip(sy, dx) {
    translate([bat_pos[0] + dx, bat_pos[1], 0])
        scale([1, sy, 1]) {
            translate([-4, bat_w/2 + 1.0, floor_t]) cube([8, 1.2, 18]);
            // profile (distance from the cell axis, height): the underside stays above the cell surface
            translate([-4, 0, 0]) rotate([90, 0, 90]) linear_extrude(height = 8)
                polygon([[12.6, 11.7], [7.8, 17.3], [7.8, 17.9], [12.6, 20.5]]);
        }
}

// ESP32 snap clip: a thin post with a hook over the board edge. The board is pressed in from above,
// the sloped top of the hook pushes the post out, then the hook sits 0.3 mm above the board.
module esp_clip(side, yoff) {
    zt = floor_t + standoff_h + 1.6;       // top of the board
    translate([esp_pos[0] + side * esp_len/2, esp_pos[1] + yoff, 0])
        scale([-side, 1, 1]) {             // local +x points to the board center
            translate([-1.15, -1.25, floor_t]) cube([1.0, 2.5, zt + 1.7 - floor_t]);
            translate([0, 1.25, 0]) rotate([90, 0, 0]) linear_extrude(height = 2.5)
                polygon([[-0.15, zt + 0.3], [0.3, zt + 0.3], [0.3, zt + 0.7], [-0.15, zt + 1.5]]);
        }
}

floor_lightening = false;   // solid floor; true cuts round lightening holes

// lightening holes in the floor, only where nothing stands on it
function floor_hole_ok(x, y) =
    !(use_battery && x > -42 && x < 34 && y < -4) &&                                                         // battery saddle
    min([for (p = esp_holes) norm([x, y] - [esp_pos[0] + p[0], esp_pos[1] + p[1]])]) > 8 &&   // ESP32 pegs
    norm([x, y] - [amp_pos[0], amp_pos[1]]) > 13 &&                                           // amp standoffs
    min([for (p = wire_posts) norm([x, y] - p)]) > 9;                                         // wire posts

module floor_holes() {
    for (x = [-36:14:36], y = [-20:14:20])
        if (floor_lightening && floor_hole_ok(x, y))
            translate([x, y, -1]) cylinder(d = 9, h = floor_t + 2, $fn = 24);
}

module body() {
    difference() {
        union() {
            difference() {
                rrect(L, W, H);
                translate([0, 0, floor_t]) rrect(L - 2*wall, W - 2*wall, H);
            }
            // 18650 saddle and its snap clips
            if (use_battery) {
                battery_saddle();
                for (sy = [-1, 1], dx = [-17.6, 17.6]) battery_clip(sy, dx);
            }
            // ESP32 pegs: the board rests on them
            for (p = esp_holes)
                translate([esp_pos[0] + p[0], esp_pos[1] + p[1], floor_t])
                    cylinder(d = 6, h = standoff_h, $fn = 24);
            // ESP32 snap clips, no screws
            for (c = esp_clips) esp_clip(c[0], c[1]);
            // wire posts: a small cone lip at the top keeps the wire from slipping off
            for (p = wire_posts)
                translate([p[0], p[1], floor_t - 0.1]) {
                    cylinder(d = wire_post_d, h = wire_post_h, $fn = 32);
                    translate([0, 0, wire_post_h]) cylinder(d1 = wire_post_d, d2 = wire_post_d + 2.4, h = 1.2, $fn = 32);
                }
            // speaker slide channel: two side ribs and a bottom stop on the inner face of the front wall.
            // the frame is lowered in from the open top; the lid holds it at the top
            for (sx = [-1, 1]) {
                translate([spk_x + sx * (spk_w/2 + 1.5) - 1, -W/2 + wall, spk_z - spk_h/2 - 1.5])
                    cube([2, 5, spk_h/2 + 1.5]);
                // flex arm continues the rib upward, 1 mm off the wall so that it springs sideways
                translate([spk_x + sx * (spk_w/2 + 1.5) - 1, -W/2 + wall + 1.0, spk_z])
                    cube([2, 3.5, spk_h/2 + 0.75]);
                // hook over the top corner of the frame; the sloped top pushes the arm out when the frame goes in
                translate([spk_x + sx * (spk_w/2 + 0.5), -W/2 + wall + 4.5, 0])
                    scale([-sx, 1, 1]) rotate([90, 0, 0]) linear_extrude(height = 3.5)
                        polygon([[0, spk_z + spk_h/2 + 0.2], [0.9, spk_z + spk_h/2 + 0.2],
                                 [0.9, spk_z + spk_h/2 + 0.4], [0, spk_z + spk_h/2 + 0.75]]);
            }
            translate([spk_x - spk_w/2 - 0.5, -W/2 + wall, spk_z - spk_h/2 - 1.5])
                cube([spk_w + 1.0, 5, 1.5]);
            // MAX98357A amp: two pins go through the board holes (the board is pressed on, friction holds it),
            // two support posts under the far edge
            for (sx = [-1, 1]) {
                translate([amp_pos[0] + sx * amp_hole_dx / 2, amp_pos[1] - amp_h/2 + 2.2, floor_t]) {
                    cylinder(d = 6, h = standoff_h, $fn = 24);
                    cylinder(d = 2.6, h = standoff_h + 2.6, $fn = 24);
                }
                translate([amp_pos[0] + sx * amp_hole_dx / 2, amp_pos[1] + amp_h/2 - 2.5, floor_t])
                    cylinder(d = 5, h = standoff_h, $fn = 24);
            }
        }
        // pockets in the walls where the lid hooks click in
        for (t = snap_tabs)
            at_snap_tab(t)
                translate([-(t[3] + 1) / 2, -0.5, -tab_len - 0.2]) cube([t[3] + 1, 1.5, 2.0]);
        // USB opening on the right wall, at the ESP32 USB connector
        translate([L/2 - wall/2, esp_pos[1], 10.5]) cube([wall * 3, 11, 7], center = true);
        // wire exit low in the right wall, level with the middle wire post
        translate([L/2 - wall/2, wire_posts[1][1], wire_hole_z]) rotate([0, 90, 0]) cylinder(d = wire_hole_d, h = wall * 3, center = true, $fn = 32);
        floor_holes();
        // speaker grille: oval pattern of holes on the front wall, sized to the cone
        for (i = [-6:6], j = [-2:2])
            if ((i*4.5/27)*(i*4.5/27) + (j*4.5/10)*(j*4.5/10) <= 1)
                translate([spk_x + i * 4.5, -W/2 + wall/2, spk_z + j * 4.5])
                    rotate([90, 0, 0]) cylinder(d = 2.5, h = wall * 3, center = true, $fn = 12);
    }
}

// four vent grilles, same pattern as the mic grille, symmetric about both axes
module grille(c) {
    for (i = [-1:1], j = [-1:1])
        translate([c[0] + i * 3.6, c[1] + j * 3.6, H - 0.1])
            cylinder(d = 2.2, h = lid_t + 1, $fn = 16);
}

module lid_holes() {
    for (c = [[24, 26], [37, 26], [24, 6]]) grille(c);
}

module snap_tab(tw) {
    // tab: continues the skirt downward, thin so that it flexes
    translate([-tw/2, -0.2 - tab_t, -tab_len]) cube([tw, tab_t, tab_len]);
    // hook: sloped lower side slides over the wall, flat upper side holds in the pocket
    translate([-tw/2, 0, 0]) rotate([90, 0, 90]) linear_extrude(height = tw)
        polygon([[-0.2, -tab_len], [-0.2 + hook_d, -tab_len + 1.0], [-0.2 + hook_d, -tab_len + 1.6], [-0.2, -tab_len + 1.6]]);
}

module lid() {
    difference() {
        union() {
            // top plate
            translate([0, 0, H]) rrect(L, W, lid_t);
            // skirt that goes inside the walls
            translate([0, 0, H - lip_h])
                difference() {
                    rrect(L - 2*wall - 2*clr, W - 2*wall - 2*clr, lip_h);
                    translate([0, 0, -1])
                        rrect(L - 2*wall - 2*clr - 2*1.2, W - 2*wall - 2*clr - 2*1.2, lip_h + 2);
                }
            // snap tabs around the lid
            for (t = snap_tabs) at_snap_tab(t) snap_tab(t[3]);
            // tact switch holder: four thin legs, a hook on each goes under the body edge of the switch
            for (a = [0, 90, 180, 270])
                translate([btn2[0], btn2[1], H - (tact_gap + tact_body_h + 1.6)])
                    rotate([0, 0, a]) {
                        translate([tact / 2 + 0.4, -2, 0]) cube([1.2, 4, tact_gap + tact_body_h + 1.6]);
                        translate([0, 2, 0]) rotate([90, 0, 0]) linear_extrude(height = 4)
                            polygon([[tact / 2 + 1.0, 0], [tact / 2 - 0.3, 1.0], [tact / 2 - 0.3, 1.6], [tact / 2 + 1.0, 1.6]]);
                    }
            // microphone holder: four thin legs under the grille. Each leg has a hook that
            // goes under the board edge; the board is pushed in from below and rests on the hooks
            for (a = [0, 90, 180, 270])
                translate([mic_c[0], mic_c[1], H - mic_h])
                    rotate([0, 0, a]) {
                        translate([mic_d / 2 + 0.2, -1.2, 0]) cube([1.2, 2.4, mic_h]);
                        translate([0, 1.2, 0]) rotate([90, 0, 0]) linear_extrude(height = 2.4)
                            polygon([[mic_d / 2 + 0.45, 0], [mic_d / 2 - 0.3, 1.0], [mic_d / 2 - 0.3, 1.6], [mic_d / 2 + 0.45, 1.6]]);
                    }
        }
        // tact switch cap hole
        translate([btn2[0], btn2[1], H - 1]) cylinder(d = btn2_hole_d, h = lid_t + 2, $fn = 40);
        // slits in the skirt on both sides of each tab so that the tab flexes from the plate
        for (t = snap_tabs)
            at_snap_tab(t)
                for (sx = [-1, 1])
                    translate([sx * (t[3]/2 + 0.4) - 0.4, -1.8, -lip_h - 0.1]) cube([0.8, 1.8, lip_h + 0.1]);
        // arcade button: a through hole for the housing, the button sinks into the lid
        translate([btn_big[0], btn_big[1], H - 1]) cylinder(d = btn_hole_d, h = lid_t + 2, $fn = 96);
        // LED hole, off: the lamp is inside the arcade button
        if (use_led_hole) translate([led[0], led[1], H - 1]) cylinder(d = led_d, h = lid_t + 2, $fn = 24);
        // microphone port: same grille as the vents, over the sound hole
        grille(mic_c);
        lid_holes();
    }
}

// lid turned upside down: plate on the bed, skirt up
module lid_print() {
    translate([0, 0, H + lid_t]) rotate([180, 0, 0]) lid();
}

if (part == "body") body();
else if (part == "lid") lid_print();
else if (part == "print") { body(); translate([L + 15, 0, 0]) lid_print(); }   // both parts side by side for the slicer
else { body(); lid(); }
