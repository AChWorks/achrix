#!/usr/bin/env python3
# SPDX-License-Identifier: MPL-2.0
"""Author synthetic complete fixtures; tooling is optional and never imported by Media.
Run with PYTHONPATH (Pillow), FFMPEG, SOFFICE and SEVENZIP paths. LibreOffice
must be an isolated, authenticated extraction; only the XML authored here is opened.
"""
import os, pathlib, struct, subprocess, tempfile, zipfile, zlib
from PIL import Image
HERE = pathlib.Path(__file__).resolve().parent
def command(*args, **kwargs):
    subprocess.run([str(x) for x in args], check=True, **kwargs)
def early_cfb_directory(path):
    """Relayout only these owned, no-DIFAT CFB exports; never a runtime parser.
    Stream contents, directory records and small-sector indices are unchanged.
    """
    raw=bytearray(path.read_bytes())
    assert raw[:8]==b"\xd0\xcf\x11\xe0\xa1\xb1\x1a\xe1"
    sector_size=1<<struct.unpack_from("<H",raw,30)[0]
    assert sector_size==512 and struct.unpack_from("<I",raw,72)[0]==0
    def get(offset): return struct.unpack_from("<I",raw,offset)[0]
    count=(len(raw)-512)//sector_size
    assert len(raw)==512+count*sector_size
    fat_sectors=[get(76+4*i) for i in range(get(44))]
    fat=list(struct.unpack("<"+"I"*(len(fat_sectors)*128),b"".join(raw[(sid+1)*512:(sid+2)*512] for sid in fat_sectors)))
    directory=[];sid=get(48)
    while sid<0xfffffffa:
        assert sid<count and sid not in directory
        directory.append(sid);sid=fat[sid]
    order=directory+fat_sectors+[sid for sid in range(count) if sid not in directory+fat_sectors]
    positions={old:new for new,old in enumerate(order)}
    def mapped(sid):return positions[sid] if sid<0xfffffffa else sid
    for offset in [48,60,68]:
        struct.pack_into("<I",raw,offset,mapped(get(offset)))
    for i in range(109):
        struct.pack_into("<I",raw,76+4*i,mapped(get(76+4*i)))
    # All regular-sector FAT chains move with the permutation.
    new_fat=[0xffffffff]*len(fat)
    for old in range(count):
        new_fat[positions[old]]=mapped(fat[old])
    for i,sid in enumerate(fat_sectors):
        struct.pack_into("<"+"I"*128,raw,(sid+1)*512,*new_fat[i*128:(i+1)*128])
    # Root stream and long user streams use regular-sector start pointers.
    # Small user streams use mini-sector indices and stay untouched.
    for sid in directory:
        for offset in range((sid+1)*512,(sid+2)*512,128):
            kind=raw[offset+66]
            size=struct.unpack_from("<Q",raw,offset+120)[0]
            if kind==5 or kind==2 and size>=get(56):
                struct.pack_into("<I",raw,offset+116,mapped(get(offset+116)))
    path.write_bytes(raw[:512]+b"".join(raw[(sid+1)*512:(sid+2)*512] for sid in order))

def main():
    image = Image.new("RGB", (32, 32), (31, 67, 103))
    for extension, format in [("png","PNG"),("jpg","JPEG"),("gif","GIF"),("webp","WEBP"),("avif","AVIF"),("bmp","BMP"),("tif","TIFF"),("ico","ICO")]:
        image.save(HERE / ("sample."+extension), format=format)
    (HERE/"sample.txt").write_text("AChrix owned media fixture\nمتن نمونه\n")
    (HERE/"sample.csv").write_text("name,value\nalpha,1\nbeta,2\n")
    with zipfile.ZipFile(HERE/"sample.zip","w",compression=zipfile.ZIP_STORED) as archive:
        info=zipfile.ZipInfo("fixture.txt", (2026,1,1,0,0,0))
        archive.writestr(info,b"AChrix owned media fixture\n")
    # A complete RAR 4 archive, one stored file, including header/file CRCs and
    # end marker. Validate with 7zip; no upstream payload is redistributed.
    def rar_header(type,flags,body):
        raw=struct.pack("<BHH",type,flags,len(body)+7)+body
        return struct.pack("<H",zlib.crc32(raw)&0xffff)+raw
    name=b"fixture.txt"; body=b"AChrix owned media fixture\n"
    rar=b"Rar!\x1a\x07\x00"+rar_header(0x73,0,b"\0"*6)
    rar+=rar_header(0x74,0x8000,struct.pack("<IIBIIBBHI",len(body),len(body),3,zlib.crc32(body),0x5c210000,20,0x30,len(name),0x81a4)+name)+body
    rar+=rar_header(0x7b,0,b"")
    (HERE/"sample.rar").write_bytes(rar)
    command(os.environ["SEVENZIP"], "a", "-t7z", "-mx=1", HERE/"sample.7z", HERE/"sample.txt")
    for extension in ["zip","rar","7z"]:
        command(os.environ["SEVENZIP"], "t", HERE/("sample."+extension))
    ffmpeg=os.environ["FFMPEG"]
    for extension,codec,args in [
        ("mp4","libx264",["-pix_fmt","yuv420p"]),
        ("mov","libx264",["-pix_fmt","yuv420p"]),
        ("webm","libvpx",["-pix_fmt","yuv420p"]),
        ("mkv","ffv1",[]),("avi","rawvideo",["-pix_fmt","bgr24"]),
        ("mpg","mpeg1video",["-pix_fmt","yuv420p"]),
        ("ogv","libtheora",["-pix_fmt","yuv420p"])]:
        command(ffmpeg,"-hide_banner","-loglevel","error","-y","-f","lavfi","-i","color=c=0x1f4367:s=32x32:r=25","-t","0.12","-an","-c:v",codec,*args,HERE/("sample."+extension))
    for extension,codec,args in [
        ("mp3","libmp3lame",[]),("m4a","aac",[]),("ogg","libvorbis",[]),
        ("wav","pcm_s16le",[]),("flac","flac",[]),("aac","aac",["-f","adts"])]:
        command(ffmpeg,"-hide_banner","-loglevel","error","-y","-f","lavfi","-i","sine=frequency=440:sample_rate=44100","-t","0.12","-vn","-c:a",codec,*args,HERE/("sample."+extension))
    # Successful complete decoding, rather than mere signature matching, is
    # the generation-time audio/video validity check.
    for extension in ["mp4","mov","webm","mkv","avi","mpg","ogv","mp3","m4a","ogg","wav","flac","aac"]:
        command(ffmpeg,"-hide_banner","-loglevel","error","-xerror","-i",HERE/("sample."+extension),"-f","null","-")
    for extension in ["png","jpg","gif","webp","avif","bmp","tif","ico"]:
        with Image.open(HERE/("sample."+extension)) as decoded:
            decoded.load()
            assert decoded.size==(32,32)
    # Flat OpenDocument authoring is complete and excludes executable content,
    # links or macros. LibreOffice exports genuine legacy and packaged formats.
    namespaces='xmlns:office="urn:oasis:names:tc:opendocument:xmlns:office:1.0" xmlns:text="urn:oasis:names:tc:opendocument:xmlns:text:1.0" xmlns:table="urn:oasis:names:tc:opendocument:xmlns:table:1.0" xmlns:draw="urn:oasis:names:tc:opendocument:xmlns:drawing:1.0" xmlns:svg="urn:oasis:names:tc:opendocument:xmlns:svg-compatible:1.0"'
    documents=[
        ("fodt","text",'<office:text><text:p>AChrix owned media fixture</text:p></office:text>',["doc","docx","odt","pdf"]),
        ("fods","spreadsheet",'<office:spreadsheet><table:table table:name="Fixture"><table:table-row><table:table-cell office:value-type="string"><text:p>AChrix owned media fixture</text:p></table:table-cell></table:table-row></table:table></office:spreadsheet>',["xls","xlsx","ods"]),
        ("fodp","presentation",'<office:presentation><draw:page draw:name="Fixture"><draw:frame svg:x="1cm" svg:y="1cm" svg:width="15cm" svg:height="5cm"><draw:text-box><text:p>AChrix owned media fixture</text:p></draw:text-box></draw:frame></draw:page></office:presentation>',["ppt","pptx","odp"])
    ]
    with tempfile.TemporaryDirectory(prefix="achrix-owned-documents-") as temporary:
        root=pathlib.Path(temporary)
        for extension,kind,body,outputs in documents:
            source=root/("sample."+extension)
            source.write_text('<?xml version="1.0" encoding="UTF-8"?><office:document '+namespaces+' office:version="1.2" office:mimetype="application/vnd.oasis.opendocument.'+kind+'"><office:body>'+body+'</office:body></office:document>')
            for output in outputs:
                command(os.environ["SOFFICE"],"-env:UserInstallation="+(root/"profile").as_uri(),"--headless","--nologo","--nodefault","--nofirststartwizard","--convert-to",output,"--outdir",HERE,source)
                assert (HERE/("sample."+output)).stat().st_size>100
                if output in ["doc","xls","ppt"]:
                    (HERE/("directory-beyond-prefix."+output)).write_bytes((HERE/("sample."+output)).read_bytes())
                    early_cfb_directory(HERE/("sample."+output))
        # Reopen only our own legacy and packaged files, export text or PDF;
        # failures cannot be treated as successful format evidence.
        for extension in ["doc","docx","odt","xls","xlsx","ods","ppt","pptx","odp"]:
            out=root/extension;out.mkdir()
            command(os.environ["SOFFICE"],"-env:UserInstallation="+(root/"profile").as_uri(),"--headless","--convert-to","pdf","--outdir",out,HERE/("sample."+extension))
            assert (out/"sample.pdf").stat().st_size>100
if __name__=="__main__":
    main()
