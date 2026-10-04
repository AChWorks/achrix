// SPDX-License-Identifier: MPL-2.0
package media

import (
 "bytes"
 "context"
 "encoding/binary"
)

func (p *imageProfile) inspectJPEG(ctx context.Context, source []byte) error {
 if len(source)<4 || source[0]!=255 || source[1]!=216 { return ErrInput }
 offset, segments, scans, metadata := 2,1,0,0
 entropy, frame, jfif, jfxx, adobe, exifSeen := false,false,false,false,false,false
 var sof, transform byte
 var identifiers [3]byte
 var h, v [3]byte
 components := 0
 quant16 := false
 var exif []byte
 for offset < len(source) {
  if err:=ctx.Err();err!=nil{return err}
  if source[offset]!=255 {
   if !entropy { return ErrInput }
   offset++;continue
  }
  offset++
  for offset<len(source) && source[offset]==255 { offset++ }
  if offset>=len(source) { return ErrInput }
  marker:=source[offset];offset++
  if entropy && marker==0 { continue }
  if entropy && marker>=208 && marker<=215 { continue }
  if marker==0 || marker==216 || marker>=208 && marker<=215 { return ErrInput }
  entropy=false
  segments++;if segments>maxImageSegments{return ErrInput}
  if marker==217 {
   if !frame || scans==0 || offset!=len(source) || sof==192 && quant16 { return ErrInput }
   rgbIDs:=components==3 && identifiers==[3]byte{'R','G','B'}
   if components==1 {
    if adobe { return ErrInput }; p.jpegModel="gray";p.jpeg444=true
   } else {
    if jfif && (rgbIDs || adobe && transform==0) || rgbIDs && adobe && transform!=0 { return ErrInput }
    if adobe && transform==0 || rgbIDs { p.jpegModel="rgb" } else { p.jpegModel="ycbcr" }
    if h[1]!=1 || h[2]!=1 || v[1]!=1 || v[2]!=1 { return ErrInput }
    if !((h[0]==1 || h[0]==2 || h[0]==4) && (v[0]==1 || v[0]==2)) { return ErrInput }
    p.jpeg444=h[0]==1 && v[0]==1
    if p.jpegModel=="rgb" && !p.jpeg444 { return ErrInput }
   }
   if len(exif)!=0 { if err:=p.inspectExif(ctx,exif);err!=nil{return err} }
   return nil
  }
  if len(source)-offset<2 { return ErrInput }
  length:=int(binary.BigEndian.Uint16(source[offset:]));if length<2 || length>len(source)-offset { return ErrInput }
  data:=source[offset+2:offset+length];offset+=length
  if marker>=224 && marker<=239 || marker==254 {
   if length+2>maxImageMetadata-metadata{return ErrInput};metadata+=length+2
  }
  switch marker {
  case 192,193,194:
   if frame || scans!=0 || len(data)<6 || data[0]!=8 {return ErrInput}
   frame,sof=true,marker
   p.height,p.width=int(binary.BigEndian.Uint16(data[1:3])),int(binary.BigEndian.Uint16(data[3:5]))
   components=int(data[5]);if !publicDimensions(p.width,p.height) || components!=1 && components!=3 || len(data)!=6+3*components {return ErrInput}
   for i:=0;i<components;i++ {
    identifiers[i]=data[6+3*i];h[i],v[i]=data[7+3*i]>>4,data[7+3*i]&15
    if h[i]==0 || h[i]>4 || v[i]==0 || v[i]>4 || data[8+3*i]>3 {return ErrInput}
    for j:=0;j<i;j++ {if identifiers[i]==identifiers[j]{return ErrInput}}
   }
   if components==1 && (h[0]!=1 || v[0]!=1) {return ErrInput}
   // The native decoder interprets these component IDs without guessing a
   // generic three-channel color space. Adobe's explicit transform takes
   // precedence over numeric IDs, but cannot contradict R/G/B identifiers.
   if components==3 && identifiers!=[3]byte{1,2,3} && identifiers!=[3]byte{'R','G','B'} {return ErrInput}
  case 218:
   if !frame || len(data)<4 {return ErrInput}
   count:=int(data[0]);if count<1 || count>components || len(data)!=1+2*count+3 {return ErrInput}
   selected:=make(map[byte]bool)
   for i:=0;i<count;i++ {
    id,table:=data[1+2*i],data[2+2*i];found:=false
    for j:=0;j<components;j++{if identifiers[j]==id{found=true}}
    if !found || selected[id] || table>>4>3 || table&15>3 {return ErrInput};selected[id]=true
   }
   ss,se,approx:=data[len(data)-3],data[len(data)-2],data[len(data)-1]
   if sof!=194 {if ss!=0 || se!=63 || approx!=0{return ErrInput}} else {
    if ss>se || se>63 || ss==0 && se!=0 || ss!=0 && count!=1 || approx>>4>13 || approx&15>13 {return ErrInput}
   }
   scans++;if scans>64{return ErrInput};entropy=true
  case 219:
   if len(data)==0{return ErrInput}
   for len(data)>0 {
    precision,id:=data[0]>>4,data[0]&15;if precision>1 || id>3{return ErrInput}
    size:=65;if precision==1{size=129;quant16=true};if len(data)<size{return ErrInput}
    data=data[size:]
   }
  case 196:
   if len(data)==0{return ErrInput}
   for len(data)>0 {
    if len(data)<17 || data[0]>>4>1 || data[0]&15>3{return ErrInput}
    count:=0;for _,n:=range data[1:17]{count+=int(n)}
    if count==0 || count>256 || len(data)<17+count{return ErrInput};data=data[17+count:]
   }
  case 221: if len(data)!=2{return ErrInput}
  case 224:
   if bytes.HasPrefix(data,[]byte("JFIF\x00")) {
    if jfif || scans!=0 || len(data)<14 || data[5]!=1 || data[6]>2 || data[7]>2 || binary.BigEndian.Uint16(data[8:10])==0 || binary.BigEndian.Uint16(data[8:10])!=binary.BigEndian.Uint16(data[10:12]) || len(data)!=14+3*int(data[12])*int(data[13]) {return ErrInput}
    jfif=true
   } else if bytes.HasPrefix(data,[]byte("JFXX\x00")) {
    if !jfif || jfxx || scans!=0 || len(data)<6{return ErrInput};jfxx=true
    switch data[5] {
    case 16: if len(data)<10 || !bytes.Equal(data[6:8],[]byte{255,216}) || !bytes.Equal(data[len(data)-2:],[]byte{255,217}) {return ErrInput}
    case 17,19:
     if len(data)<8 || data[6]==0 || data[7]==0{return ErrInput}
     pixels:=int(data[6])*int(data[7]);size:=8+3*pixels;if data[5]==17{size=8+768+pixels};if len(data)!=size{return ErrInput}
    default:return ErrInput
    }
   } else {return ErrInput}
  case 225:
   if exifSeen || !bytes.HasPrefix(data,[]byte("Exif\x00\x00")) || len(data)<=6{return ErrInput};exifSeen=true;exif=data[6:]
  case 238:
   if adobe || scans!=0 || len(data)!=12 || !bytes.Equal(data[:5],[]byte("Adobe")) || binary.BigEndian.Uint16(data[5:7])!=100 && binary.BigEndian.Uint16(data[5:7])!=101 || binary.BigEndian.Uint16(data[9:11])!=0 || data[11]>1 {return ErrInput}
   adobe,transform=true,data[11]
  case 254: // COM is nonessential and discarded.
  default:return ErrInput
  }
 }
 return ErrInput
}
func jpegSegment(marker byte,data []byte) []byte {
 result:=make([]byte,len(data)+4);result[0],result[1]=255,marker;binary.BigEndian.PutUint16(result[2:4],uint16(len(data)+2));copy(result[4:],data);return result
}
func (p imageProfile) jpegOutputDeclarations() []byte {
 result:=jpegSegment(224,[]byte{'J','F','I','F',0,1,2,0,0,1,0,1,0,0})
 if p.colorBasis()=="declared-srgb" {
  // Fresh little-endian IFD0 pointer and one Exif ColorSpace=1 declaration.
  tiff:=make([]byte,44);copy(tiff,[]byte{'I','I',42,0,8,0,0,0});binary.LittleEndian.PutUint16(tiff[8:],1)
  binary.LittleEndian.PutUint16(tiff[10:],0x8769);binary.LittleEndian.PutUint16(tiff[12:],4);binary.LittleEndian.PutUint32(tiff[14:],1);binary.LittleEndian.PutUint32(tiff[18:],26)
  binary.LittleEndian.PutUint16(tiff[26:],1);binary.LittleEndian.PutUint16(tiff[28:],0xa001);binary.LittleEndian.PutUint16(tiff[30:],3);binary.LittleEndian.PutUint32(tiff[32:],1);binary.LittleEndian.PutUint16(tiff[36:],1)
  result=append(result,jpegSegment(225,append([]byte("Exif\x00\x00"),tiff...))...)
 }
 return result
}
