package lcf

import (
	"strconv"
	"strings"
)

// The table below is the schema of the save structures of RPG Maker 2000 and 2003, as the
// EasyRPG project documents them in liblcf (https://github.com/EasyRPG/liblcf, MIT license):
// generator/csv/fields.csv, fields_easyrpg.csv and structs.csv. Only the ids, names and types
// of the fields are taken from there; none of liblcf's code is used.
//
// Format of the table: a line "struct Name [id] [base=Other]" starts a struct ("id" means that
// the elements of an array of this struct are numbered in the file; "base" is a struct whose
// fields this one has too). Each field line is "<chunk id> <name> <type> [2k3]", where 2k3
// marks a field that only RPG Maker 2003 writes. The types are:
//
//	i32      Int32, or an enum or a reference: a BER number (see readBER)
//	bool     Boolean: one byte, 0 or 1
//	str      String: bytes in the code page of the game
//	f64      Double: 8 bytes, little endian
//	i8, u8   one byte
//	u32      4 bytes, little endian
//	flags    a byte of bit flags
//	vbool    Vector<Boolean>: one byte per element
//	v16      Vector<Int16>: 2 bytes per element, little endian
//	v32      Vector<Int32>: 4 bytes per element, little endian
//	v8       Vector<UInt8>: one byte per element
//	s:Name   a struct: its chunks, ended by a 0 id
//	a:Name   Array<Name>: a BER count, then each element (a BER number if the struct has "id",
//	         then its chunks, ended by a 0 id)
//	count    the number of elements of the vector chunk that follows; kept, not shown
//	bytes    kept as bytes
const schemaSpec = `
struct SaveTitle
	0x01 timestamp f64
	0x0B hero_name str
	0x0C hero_level i32
	0x0D hero_hp i32
	0x15 face1_name str
	0x16 face1_id i32
	0x17 face2_name str
	0x18 face2_id i32
	0x19 face3_name str
	0x1A face3_id i32
	0x1B face4_name str
	0x1C face4_id i32
struct SaveSystem
	0x01 scene i32
	0x0B frame_count i32
	0x15 graphics_name str
	0x16 message_stretch i32
	0x17 font_id i32
	0x1F switches_count count
	0x20 switches vbool
	0x21 variables_count count
	0x22 variables v32
	0x29 message_transparent i32
	0x2A message_position i32
	0x2B message_prevent_overlap i32
	0x2C message_continue_events i32
	0x33 face_name str
	0x34 face_id i32
	0x35 face_right bool
	0x36 face_flip bool
	0x37 event_message_active bool
	0x3D music_stopping bool
	0x47 title_music s:Music
	0x48 battle_music s:Music
	0x49 battle_end_music s:Music
	0x4A inn_music s:Music
	0x4B current_music s:Music
	0x4C before_vehicle_music s:Music
	0x4D before_battle_music s:Music
	0x4E stored_music s:Music
	0x4F boat_music s:Music
	0x50 ship_music s:Music
	0x51 airship_music s:Music
	0x52 gameover_music s:Music
	0x5B cursor_se s:Sound
	0x5C decision_se s:Sound
	0x5D cancel_se s:Sound
	0x5E buzzer_se s:Sound
	0x5F battle_se s:Sound
	0x60 escape_se s:Sound
	0x61 enemy_attack_se s:Sound
	0x62 enemy_damaged_se s:Sound
	0x63 actor_damaged_se s:Sound
	0x64 dodge_se s:Sound
	0x65 enemy_death_se s:Sound
	0x66 item_se s:Sound
	0x6F transition_out i8
	0x70 transition_in i8
	0x71 battle_start_fadeout i8
	0x72 battle_start_fadein i8
	0x73 battle_end_fadeout i8
	0x74 battle_end_fadein i8
	0x79 teleport_allowed bool
	0x7A escape_allowed bool
	0x7B save_allowed bool
	0x7C menu_allowed bool
	0x7D background str
	0x83 save_count i32
	0x84 save_slot i32
	0x8C atb_mode i32 2k3
	0x24 maniac_strings bytes
	0x2D maniac_message_window_width i32
	0x2E maniac_message_window_height i32
	0x2F maniac_message_font_name str
	0x30 maniac_message_font_size i32
	0x32 maniac_message_hook_flags bytes
	0x40 maniac_message_face_width i32
	0x41 maniac_message_face_height i32
	0x42 maniac_message_hook_common_event_id i32
	0x43 maniac_message_hook_callback_system_variable i32
	0x44 maniac_message_hook_callback_system_string_variable i32
	0x45 maniac_message_hook_callback_user_variable i32
	0x46 maniac_message_hook_callback_user_string_variable i32
	0x85 maniac_battle_origin i32
	0x88 maniac_frameskip i32
	0x89 maniac_picture_limit i32
	0x8A maniac_options v8
	0x8B maniac_joypad_bindings v8
	0x8E maniac_message_spacing_char i32
	0x8F maniac_message_spacing_line i32
struct SaveScreen
	0x01 tint_finish_red i32
	0x02 tint_finish_green i32
	0x03 tint_finish_blue i32
	0x04 tint_finish_sat i32
	0x0B tint_current_red f64
	0x0C tint_current_green f64
	0x0D tint_current_blue f64
	0x0E tint_current_sat f64
	0x0F tint_time_left i32
	0x14 flash_continuous bool
	0x15 flash_red i32
	0x16 flash_green i32
	0x17 flash_blue i32
	0x18 flash_current_level f64
	0x19 flash_time_left i32
	0x1E shake_continuous bool
	0x1F shake_strength i32
	0x20 shake_speed i32
	0x21 shake_position i32
	0x22 shake_position_y i32
	0x23 shake_time_left i32
	0x29 pan_x i32
	0x2A pan_y i32
	0x2B battleanim_id i32
	0x2C battleanim_target i32
	0x2D battleanim_frame i32
	0x2E battleanim_active bool
	0x2F battleanim_global bool
	0x30 weather i32
	0x31 weather_strength i32
struct SavePicture id
	0x01 name str
	0x02 start_x f64
	0x03 start_y f64
	0x04 current_x f64
	0x05 current_y f64
	0x06 fixed_to_map bool
	0x07 current_magnify f64
	0x08 current_top_trans f64
	0x09 use_transparent_color bool
	0x0B current_red f64
	0x0C current_green f64
	0x0D current_blue f64
	0x0E current_sat f64
	0x0F effect_mode i32
	0x10 current_effect_power f64
	0x12 current_bot_trans f64
	0x13 spritesheet_cols i32 2k3
	0x14 spritesheet_rows i32 2k3
	0x15 spritesheet_frame i32 2k3
	0x16 spritesheet_speed i32 2k3
	0x17 frames i32 2k3
	0x18 spritesheet_play_once bool 2k3
	0x19 map_layer i32
	0x1A battle_layer i32
	0x1B flags flags
	0x1F finish_x f64
	0x20 finish_y f64
	0x21 finish_magnify i32
	0x22 finish_top_trans i32
	0x23 finish_bot_trans i32
	0x29 finish_red i32
	0x2A finish_green i32
	0x2B finish_blue i32
	0x2C finish_sat i32
	0x2E finish_effect_power i32
	0x33 time_left i32
	0x34 current_rotation f64
	0x35 current_waver i32
	0xC8 easyrpg_flip i32 2k3
	0xC9 easyrpg_blend_mode i32 2k3
	0xCA easyrpg_type i32 2k3
	0x0A maniac_current_magnify_height f64
	0x1C maniac_image_data v8
	0x24 maniac_finish_magnify_height i32
struct SavePartyLocation base=SaveMapEventBase
	0x65 boarding bool
	0x66 aboard bool
	0x67 vehicle i32
	0x68 unboarding bool
	0x69 preboard_move_speed i32
	0x6C menu_calling bool
	0x6F pan_state i32
	0x70 pan_current_x i32
	0x71 pan_current_y i32
	0x72 pan_finish_x i32
	0x73 pan_finish_y i32
	0x79 pan_speed i32
	0x7C total_encounter_rate i32
	0x7D encounter_calling bool
	0x83 map_save_count i32
	0x84 database_save_count i32
	0x8D maniac_horizontal_pan_speed f64
	0x8E maniac_vertical_pan_speed f64
struct SaveVehicleLocation base=SaveMapEventBase
	0x65 vehicle i32
	0x6A remaining_ascent i32
	0x6B remaining_descent i32
	0x6F orig_sprite_name str
	0x70 orig_sprite_id i32
struct SaveActor id
	0x01 name str
	0x02 title str
	0x0B sprite_name str
	0x0C sprite_id i32
	0x0D transparency i32
	0x15 face_name str
	0x16 face_id i32
	0x1F level i32
	0x20 exp i32
	0x21 hp_mod i32
	0x22 sp_mod i32
	0x29 attack_mod i32
	0x2A defense_mod i32
	0x2B spirit_mod i32
	0x2C agility_mod i32
	0x33 skills_count count
	0x34 skills v16
	0x3D equipped v16
	0x47 current_hp i32
	0x48 current_sp i32
	0x50 battle_commands v32 2k3
	0x51 status_count count
	0x52 status v16
	0x53 changed_battle_commands bool
	0x5A class_id i32
	0x5B row i32 2k3
	0x5C two_weapon bool 2k3
	0x5D lock_equipment bool 2k3
	0x5E auto_battle bool 2k3
	0x5F super_guard bool 2k3
	0x60 battler_animation i32 2k3
struct SaveInventory
	0x01 party_count count
	0x02 party v16
	0x0B item_ids_count count
	0x0C item_ids v16
	0x0D item_counts v8
	0x0E item_usage v8
	0x15 gold i32
	0x17 timer1_frames i32
	0x18 timer1_active bool
	0x19 timer1_visible bool
	0x1A timer1_battle bool
	0x1B timer2_frames i32
	0x1C timer2_active bool
	0x1D timer2_visible bool
	0x1E timer2_battle bool
	0x20 battles i32
	0x21 defeats i32
	0x22 escapes i32
	0x23 victories i32
	0x29 turns i32
	0x2A steps i32
struct SaveTarget id
	0x01 map_id i32
	0x02 map_x i32
	0x03 map_y i32
	0x04 switch_on bool
	0x05 switch_id i32
struct SaveEventExecFrame id
	0x01 commands_count count
	0x02 commands bytes
	0x0B current_command i32
	0x0C event_id i32
	0x0D triggered_by_decision_key bool
	0x15 subcommand_path_count count
	0x16 subcommand_path v8
	0x0E maniac_event_info i32
	0x0F maniac_event_id i32
	0x10 maniac_event_page_id i32
	0x11 maniac_loop_info_size i32
	0x12 maniac_loop_info v32
	0xCC easyrpg_runtime_flags bytes
struct SaveEventExecState
	0x01 stack a:SaveEventExecFrame
	0x04 show_message bool
	0x0B abort_on_escape bool
	0x0D wait_movement bool
	0x15 keyinput_wait bool
	0x16 keyinput_variable u8
	0x17 keyinput_all_directions bool
	0x18 keyinput_decision i32
	0x19 keyinput_cancel i32
	0x1A keyinput_2kshift_2k3numbers i32
	0x1B keyinput_2kdown_2k3operators i32
	0x1C keyinput_2kleft_2k3shift i32
	0x1D keyinput_2kright i32
	0x1E keyinput_2kup i32
	0x1F wait_time i32
	0x20 keyinput_time_variable i32
	0x23 keyinput_2k3down i32
	0x24 keyinput_2k3left i32
	0x25 keyinput_2k3right i32
	0x26 keyinput_2k3up i32
	0x29 keyinput_timed bool
	0x2A wait_key_enter bool
	0xC9 easyrpg_active bool
	0xCA easyrpg_string str
	0xCB easyrpg_parameters v32
	0xCC easyrpg_runtime_flags bytes
struct SaveMapEventBase
	0x01 active bool
	0x0B map_id i32
	0x0C position_x i32
	0x0D position_y i32
	0x15 direction i32
	0x16 facing i32
	0x17 anim_frame i32
	0x18 transparency i32
	0x1F remaining_step i32
	0x20 move_frequency i32
	0x21 layer i32
	0x22 overlap_forbidden bool
	0x23 animation_type i32
	0x24 lock_facing bool
	0x25 move_speed i32
	0x29 move_route s:MoveRoute
	0x2A move_route_overwrite bool
	0x2B move_route_index i32
	0x2C move_route_finished bool
	0x2E sprite_hidden bool
	0x2F move_route_through bool
	0x30 anim_paused i32
	0x33 through bool
	0x34 stop_count i32
	0x35 anim_count i32
	0x36 max_stop_count i32
	0x3D jumping bool
	0x3E begin_jump_x i32
	0x3F begin_jump_y i32
	0x47 pause bool
	0x48 flying bool
	0x49 sprite_name str
	0x4A sprite_id i32
	0x4B processed bool
	0x51 flash_red i32
	0x52 flash_green i32
	0x53 flash_blue i32
	0x54 flash_current_level f64
	0x55 flash_time_left i32
	0xC9 easyrpg_move_failure_count i32
	0xCA easyrpg_clone_map_id u32
	0xCB easyrpg_clone_event_id u32
	0xCC easyrpg_runtime_flags bytes
struct SaveMapEvent id base=SaveMapEventBase
	0x65 waiting_execution bool
	0x66 original_move_route_index i32
	0x67 triggered_by_decision_key bool
	0x6C parallel_event_execstate s:SaveEventExecState
struct SaveMapInfo
	0x01 position_x i32
	0x02 position_y i32
	0x03 encounter_steps i32
	0x05 chipset_id i32
	0x0B events a:SaveMapEvent
	0x15 lower_tiles v8
	0x16 upper_tiles v8
	0x20 parallax_name str
	0x21 parallax_horz bool
	0x22 parallax_vert bool
	0x23 parallax_horz_auto bool
	0x24 parallax_horz_speed i32
	0x25 parallax_vert_auto bool
	0x26 parallax_vert_speed i32
struct SaveCommonEvent id
	0x01 parallel_event_execstate s:SaveEventExecState
struct SavePanorama
	0x01 pan_x i32
	0x02 pan_y i32
struct Save
	0x64 title s:SaveTitle
	0x65 system s:SaveSystem
	0x66 screen s:SaveScreen
	0x67 pictures a:SavePicture
	0x68 party_location s:SavePartyLocation
	0x69 boat_location s:SaveVehicleLocation
	0x6A ship_location s:SaveVehicleLocation
	0x6B airship_location s:SaveVehicleLocation
	0x6C actors a:SaveActor
	0x6D inventory s:SaveInventory
	0x6E targets a:SaveTarget
	0x6F map_info s:SaveMapInfo
	0x70 panorama s:SavePanorama
	0x71 foreground_event_execstate s:SaveEventExecState
	0x72 common_events a:SaveCommonEvent
	0xC8 easyrpg_data s:SaveEasyRpgData
struct SaveEasyRpgData
	0x01 version i32
	0x02 codepage i32
	0x64 windows a:SaveEasyRpgWindow
struct SaveEasyRpgWindow id
	0x01 texts a:SaveEasyRpgText
	0x02 width i32
	0x03 height i32
	0x04 system_name str
	0x05 message_stretch i32
	0x06 flags bytes
struct SaveEasyRpgText
	0x01 text str
	0x02 position_x i32
	0x03 position_y i32
	0x04 font_name str
	0x05 font_size i32
	0x06 letter_spacing i32
	0x07 line_spacing i32
	0x08 flags bytes
struct Sound
	0x01 name str
	0x03 volume i32
	0x04 tempo i32
	0x05 balance i32
struct Music
	0x01 name str
	0x02 fadein i32
	0x03 volume i32
	0x04 tempo i32
	0x05 balance i32
struct MoveRoute
	0x0B move_commands_count count
	0x0C move_commands bytes
	0x15 repeat bool
	0x16 skippable bool
`

// fieldType is how the data of a chunk is read.
type fieldType uint8

const (
	ftInt32 fieldType = iota + 1
	ftBool
	ftString
	ftDouble
	ftInt8
	ftUInt8
	ftUInt32
	ftFlags
	ftVecBool
	ftVecInt16
	ftVecInt32
	ftVecUInt8
	ftStruct
	ftArray
	ftCount
	ftBytes
)

// field is one chunk of a struct.
type field struct {
	id    int
	name  string
	typ   fieldType
	sub   string // the struct of an ftStruct or ftArray
	is2k3 bool
}

// structDef is a struct of the save: a list of chunks.
type structDef struct {
	name   string
	hasID  bool // the elements of an array of this struct start with a number
	fields []*field
	byID   map[int]*field
}

// schema is the table above, parsed. It is built once and never changed.
var schema = parseSchema(schemaSpec)

var fieldTypes = map[string]fieldType{
	"i32": ftInt32, "bool": ftBool, "str": ftString, "f64": ftDouble, "i8": ftInt8, "u8": ftUInt8,
	"u32": ftUInt32, "flags": ftFlags, "vbool": ftVecBool, "v16": ftVecInt16, "v32": ftVecInt32,
	"v8": ftVecUInt8, "count": ftCount, "bytes": ftBytes,
}

// parseSchema reads the table. The table is a constant of this package, so a mistake in it is
// a mistake of the program: it panics, and TestSchema makes sure it does not.
func parseSchema(spec string) map[string]*structDef {
	defs := map[string]*structDef{}
	bases := map[string]string{}
	var cur *structDef
	for _, line := range strings.Split(spec, "\n") {
		parts := strings.Fields(line)
		if len(parts) == 0 {
			continue
		}
		if parts[0] == "struct" {
			cur = &structDef{name: parts[1], byID: map[int]*field{}}
			for _, opt := range parts[2:] {
				if opt == "id" {
					cur.hasID = true
				} else if b, ok := strings.CutPrefix(opt, "base="); ok {
					bases[cur.name] = b
				} else {
					panic("lcf: bad struct option " + opt)
				}
			}
			defs[cur.name] = cur
			continue
		}
		id, err := strconv.ParseInt(parts[0], 0, 32)
		if err != nil || cur == nil || len(parts) < 3 {
			panic("lcf: bad schema line " + line)
		}
		f := &field{id: int(id), name: parts[1], is2k3: len(parts) > 3 && parts[3] == "2k3"}
		switch {
		case strings.HasPrefix(parts[2], "s:"):
			f.typ, f.sub = ftStruct, parts[2][2:]
		case strings.HasPrefix(parts[2], "a:"):
			f.typ, f.sub = ftArray, parts[2][2:]
		default:
			t, ok := fieldTypes[parts[2]]
			if !ok {
				panic("lcf: bad schema type " + parts[2])
			}
			f.typ = t
		}
		if _, dup := cur.byID[f.id]; dup {
			panic("lcf: duplicate chunk id in " + cur.name)
		}
		cur.fields = append(cur.fields, f)
		cur.byID[f.id] = f
	}
	for name, base := range bases {
		d, b := defs[name], defs[base]
		if b == nil {
			panic("lcf: unknown base struct " + base)
		}
		// The fields of the base come first, as in liblcf.
		d.fields = append(append([]*field(nil), b.fields...), d.fields...)
		for _, f := range b.fields {
			if _, dup := d.byID[f.id]; dup {
				panic("lcf: duplicate chunk id in " + name)
			}
			d.byID[f.id] = f
		}
	}
	for _, d := range defs {
		for _, f := range d.fields {
			if f.sub != "" && defs[f.sub] == nil {
				panic("lcf: unknown struct " + f.sub)
			}
		}
	}
	return defs
}
